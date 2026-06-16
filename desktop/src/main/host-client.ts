import { execFileSync, spawn } from 'node:child_process';
import net from 'node:net';

export const PROTO_VERSION = 3;
const MAX_FRAME = 16 << 20;

export interface SessionInfo {
  name: string;
  alive: boolean;
  exitCode?: number;
  program?: string;
}

export interface WorkspaceInfo {
  id: string;
  title: string;
  program: string;
  repoPath: string;
  worktreePath: string;
  branch: string;
  sessionName: string;
  alive: boolean;
  autoYes: boolean;
  added: number;
  removed: number;
  createdUnix: number;
  runCommand: string;
  running: boolean;
  previewUrl: string;
  busy: boolean;
  waiting: boolean;
}

export interface FileDiffInfo {
  path: string;
  added: number;
  removed: number;
}

export interface Request {
  id?: number;
  method:
    | 'Hello'
    | 'CreateSession'
    | 'Attach'
    | 'Resize'
    | 'SendKeys'
    | 'KillSession'
    | 'ListSessions'
    | 'ListWorkspaces'
    | 'CreateWorkspace'
    | 'GetWorkspace'
    | 'ArchiveWorkspace'
    | 'WorkspaceDiff'
    | 'WorkspaceCommit'
    | 'WorkspacePush'
    | 'SetWorkspaceAutoYes'
    | 'StartRun'
    | 'StopRun'
    | 'WorkspaceRunOutput'
    | string;
  session?: string;
  program?: string;
  workDir?: string;
  cols?: number;
  rows?: number;
  autoYes?: boolean;
  enabled?: boolean;
  message?: string;
  data?: string;
  mode?: string;
  withANSI?: boolean;
  clientVersion?: number;
  // Workspace methods (v2)
  repoPath?: string;
  title?: string;
  baseBranch?: string;
  workspaceId?: string;
  file?: string;
  // Run methods (v3)
  command?: string;
  sinceOffset?: number;
}

export interface Response {
  id: number;
  ok: boolean;
  error?: string;
  hostVersion?: number;
  content?: string;
  exists?: boolean;
  alive?: boolean;
  updated?: boolean;
  hasPrompt?: boolean;
  sessions?: SessionInfo[];
  attachPipe?: string;
  attachToken?: string;
  // Workspace methods (v2)
  workspaces?: WorkspaceInfo[];
  workspace?: WorkspaceInfo;
  files?: FileDiffInfo[];
  diff?: string;
  // Run methods (v3). `data` is base64-encoded run output bytes on the wire.
  data?: string;
  nextOffset?: number;
  runRunning?: boolean;
  exitCode?: number;
}

type PendingCall = {
  resolve: (response: Response) => void;
  reject: (error: Error) => void;
};

export function frame(payload: Buffer): Buffer {
  const header = Buffer.alloc(4);
  header.writeUInt32BE(payload.length, 0);
  return Buffer.concat([header, payload]);
}

export class FrameDecoder {
  private buffer = Buffer.alloc(0);

  public constructor(private readonly onFrame: (payload: Buffer) => void) {}

  public push(chunk: Buffer): void {
    this.buffer = Buffer.concat([this.buffer, chunk]);
    while (this.buffer.length >= 4) {
      const length = this.buffer.readUInt32BE(0);
      if (length > MAX_FRAME) {
        throw new Error(`frame too large: ${length}`);
      }
      if (this.buffer.length < 4 + length) {
        break;
      }
      const payload = this.buffer.subarray(4, 4 + length);
      this.buffer = this.buffer.subarray(4 + length);
      this.onFrame(payload);
    }
  }
}

export class ControlClient {
  private readonly queue: PendingCall[] = [];
  private readonly decoder: FrameDecoder;
  private nextId = 0;
  private closed = false;

  public constructor(private readonly socket: net.Socket) {
    this.decoder = new FrameDecoder((payload) => {
      let response: Response;
      try {
        response = JSON.parse(payload.toString('utf8')) as Response;
      } catch {
        return;
      }
      const pending = this.queue.shift();
      if (pending) {
        pending.resolve(response);
      }
    });

    socket.on('data', (chunk: Buffer) => {
      try {
        this.decoder.push(chunk);
      } catch (error) {
        this.rejectAll(error instanceof Error ? error : new Error(String(error)));
      }
    });
    socket.once('error', (error) => this.rejectAll(error));
    socket.once('close', () => {
      this.closed = true;
      this.rejectAll(new Error('control pipe closed'));
    });
  }

  public call(request: Omit<Request, 'id'>): Promise<Response> {
    if (this.closed) {
      return Promise.reject(new Error('control pipe is closed'));
    }

    return new Promise<Response>((resolve, reject) => {
      const id = ++this.nextId;
      const payload: Request = { ...request, id };
      const pending = { resolve, reject };
      this.queue.push(pending);
      this.socket.write(frame(Buffer.from(JSON.stringify(payload), 'utf8')), (error) => {
        if (error) {
          const index = this.queue.indexOf(pending);
          if (index >= 0) {
            this.queue.splice(index, 1);
          }
          reject(error);
        }
      });
    });
  }

  public close(): void {
    this.closed = true;
    this.socket.end();
    this.rejectAll(new Error('control client closed'));
  }

  private rejectAll(error: Error): void {
    while (this.queue.length > 0) {
      this.queue.shift()?.reject(error);
    }
  }
}

export function currentUserSid(): string {
  const output = execFileSync('whoami', ['/user', '/fo', 'csv', '/nh'], {
    encoding: 'utf8',
    windowsHide: true,
  }).trim();
  const match = output.match(/(S-1-[0-9-]+)/);
  if (!match) {
    throw new Error(`could not parse SID from: ${output}`);
  }
  return match[1];
}

export function controlPipeName(): string {
  return `\\\\.\\pipe\\claudesquad-host-${currentUserSid()}`;
}

export function connectPipe(pipeName: string, timeoutMs = 2000): Promise<net.Socket> {
  return new Promise((resolve, reject) => {
    const socket = net.connect({ path: pipeName });
    const timer = setTimeout(() => {
      socket.destroy();
      reject(new Error('pipe connect timeout'));
    }, timeoutMs);

    socket.once('connect', () => {
      clearTimeout(timer);
      resolve(socket);
    });
    socket.once('error', (error) => {
      clearTimeout(timer);
      reject(error);
    });
  });
}

export async function ensureHost(csExe: string): Promise<string> {
  const pipeName = controlPipeName();
  try {
    const socket = await connectPipe(pipeName, 800);
    socket.end();
    return pipeName;
  } catch {
    // Spawn below.
  }

  const child = spawn(csExe, ['session-host'], {
    detached: true,
    stdio: 'ignore',
    windowsHide: true,
  });
  child.unref();

  const deadline = Date.now() + 10_000;
  while (Date.now() < deadline) {
    await delay(300);
    try {
      const socket = await connectPipe(pipeName, 500);
      socket.end();
      return pipeName;
    } catch {
      // Keep polling until the daemon creates the SID pipe.
    }
  }

  throw new Error('session-host did not become ready');
}

export async function connectAttachStream(attachPipe: string, attachToken: string): Promise<net.Socket> {
  const socket = await connectPipe(attachPipe);
  socket.write(frame(Buffer.from(attachToken, 'utf8')));
  return socket;
}

function delay(ms: number): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, ms));
}
