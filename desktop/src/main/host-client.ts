import { execFileSync, spawn } from 'node:child_process';
import crypto from 'node:crypto';
import { existsSync, readFileSync } from 'node:fs';
import net from 'node:net';
import os from 'node:os';
import path from 'node:path';
import { PROTO_VERSION } from '../shared/proto-version';
import { log } from './logger';
export { PROTO_VERSION };
const MAX_FRAME = 16 << 20;
const HELLO_PROOF_PREFIX = 'hangar-winhost-hello-v7\n';

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
  // Last time the agent produced output that changed its screen, Unix seconds;
  // 0 = unknown (no live session or it never changed). Mirrors proto.
  lastOutputUnix: number;
  runCommand: string;
  running: boolean;
  previewUrl: string;
  busy: boolean;
  waiting: boolean;
  regenerating: boolean;
  regenPhase?: string;
  shell: string;
}

export interface CopilotSessionInfo {
  id: string;
  name: string;
  repository: string;
  branch: string;
  originRoot: string;
  createdAt: number;
  updatedAt: number;
  inUse: boolean;
  firstMsg?: string;
}

export interface FileDiffInfo {
  path: string;
  added: number;
  removed: number;
}

export interface HostInfo {
  pipeName: string;
  pid: number;
  createdUnix: number;
  nonce: string;
  version: number;
}

// Files tab (read-only worktree browser) — shared between main and preload.
export type DirEntry = {
  name: string;
  dir: boolean;
};

export type FileContents =
  | { kind: 'text'; text: string }
  | { kind: 'binary' }
  | { kind: 'tooLarge'; size: number }
  | { kind: 'error'; message: string };

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
    | 'GenerateWorkspaceTitle'
    | 'RegenerateAgent'
    | 'ForceRegenerate'
    | 'CaptureHistory'
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
  includeScreen?: boolean;
  clientVersion?: number;
  clientNonce?: string;
  // Workspace methods (v2)
  repoPath?: string;
  title?: string;
  baseBranch?: string;
  workspaceId?: string;
  file?: string;
  // ArchiveWorkspace: when true, also delete the worktree directory and
  // its branch; when false (default), keep the worktree and branch on disk.
  deleteWorktree?: boolean;
  // Run methods (v3)
  command?: string;
  sinceOffset?: number;
  // Regenerate (v5)
  handoff?: boolean;
  // Shell selection
  shell?: string;
  // ResumeCopilotSession
  sessionId?: string;
  // Cross-repo resume confirmation (v8): acknowledge a NeedsConfirm response.
  confirmed?: boolean;
}

export interface Response {
  id: number;
  ok: boolean;
  error?: string;
  hostVersion?: number;
  hostPid?: number;
  hostCreatedUnix?: number;
  hostNonceProof?: string;
  content?: string;
  altScreen?: boolean;
  scrollbackLines?: number;
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
  // Copilot session browser (v6)
  copilotSessions?: CopilotSessionInfo[];
  skipped?: number;
  // Cross-repo resume confirmation (v8)
  needsConfirm?: boolean;
  absPath?: string;
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
  // Buffered bytes are held as a list of received chunks plus a running byte
  // count rather than a single growing Buffer. This keeps push() O(chunk):
  // we only Buffer.concat the exact bytes of a frame once it has fully
  // arrived, instead of re-concatenating the entire backlog on every socket
  // read — which is O(n^2) when one large frame (e.g. a big diff or a terminal
  // history snapshot) is split across many pipe reads.
  private chunks: Buffer[] = [];
  private buffered = 0;

  public constructor(private readonly onFrame: (payload: Buffer) => void) {}

  public push(chunk: Buffer): void {
    if (chunk.length === 0) {
      return;
    }
    this.chunks.push(chunk);
    this.buffered += chunk.length;

    while (this.buffered >= 4) {
      const length = this.peekUInt32BE();
      if (length > MAX_FRAME) {
        throw new Error(`frame too large: ${length}`);
      }
      if (this.buffered < 4 + length) {
        break;
      }
      // The full frame (4-byte length prefix + payload) is buffered: materialize
      // it once and hand the caller a Buffer view of exactly the payload bytes.
      const fullFrame = this.take(4 + length);
      this.onFrame(fullFrame.subarray(4));
    }
  }

  // Read the leading 4 bytes as a big-endian uint32 without consuming them.
  // Only called once at least 4 bytes are buffered, so the prefix is complete
  // even when it is split across multiple chunks.
  private peekUInt32BE(): number {
    const first = this.chunks[0];
    if (first.length >= 4) {
      return first.readUInt32BE(0);
    }
    let value = 0;
    let read = 0;
    for (const chunk of this.chunks) {
      for (let i = 0; i < chunk.length && read < 4; i += 1) {
        value = value * 256 + chunk[i];
        read += 1;
      }
      if (read === 4) {
        break;
      }
    }
    return value >>> 0;
  }

  // Remove and return exactly `n` bytes from the front of the buffered chunks
  // as a single contiguous Buffer, retaining any trailing remainder for the
  // next frame. Only called once `n` bytes are known to be available.
  private take(n: number): Buffer {
    this.buffered -= n;
    const first = this.chunks[0];
    if (first.length === n) {
      this.chunks.shift();
      return first;
    }
    if (first.length > n) {
      this.chunks[0] = first.subarray(n);
      return first.subarray(0, n);
    }
    const parts: Buffer[] = [];
    let collected = 0;
    while (collected < n) {
      const chunk = this.chunks[0];
      const need = n - collected;
      if (chunk.length <= need) {
        parts.push(chunk);
        collected += chunk.length;
        this.chunks.shift();
      } else {
        parts.push(chunk.subarray(0, need));
        this.chunks[0] = chunk.subarray(need);
        collected += need;
      }
    }
    return Buffer.concat(parts, n);
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

  public isClosed(): boolean {
    return this.closed;
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
  return `\\\\.\\pipe\\hangar-host-${currentUserSid()}`;
}

export function hostInfoPath(): string {
  return path.join(os.homedir(), '.hangar', 'host.json');
}

function isLowerHexBytes(value: string, nbytes: number): boolean {
  return new RegExp(`^[0-9a-f]{${nbytes * 2}}$`).test(value);
}

export function readHostInfo(): HostInfo {
  const raw = JSON.parse(readFileSync(hostInfoPath(), 'utf8')) as Partial<HostInfo>;
  return {
    pipeName: typeof raw.pipeName === 'string' ? raw.pipeName : '',
    pid: typeof raw.pid === 'number' ? raw.pid : 0,
    createdUnix: typeof raw.createdUnix === 'number' ? raw.createdUnix : 0,
    nonce: typeof raw.nonce === 'string' ? raw.nonce : '',
    version: typeof raw.version === 'number' ? raw.version : 0,
  };
}

export function processCreationUnix(pid: number): number {
  if (!Number.isSafeInteger(pid) || pid <= 0) {
    throw new Error(`invalid pid ${pid}`);
  }
  // Get-Process .StartTime is the process creation time, matching Go's
  // GetProcessTimes creation FILETIME within the ±1s tolerance enforced below.
  const output = execFileSync(
    'powershell',
    [
      '-NoProfile',
      '-NonInteractive',
      '-Command',
      `$p = Get-Process -Id ${pid} -ErrorAction SilentlyContinue; if ($p) { [DateTimeOffset]::new($p.StartTime.ToUniversalTime(), [TimeSpan]::Zero).ToUnixTimeSeconds() }`,
    ],
    // stdio stderr 'ignore' keeps PowerShell's noise out of the app console — a
    // stale host.json (e.g. after dev-worktree.ps1 force-kills the daemon) points
    // at a dead pid, and Get-Process would otherwise spew an error block. The
    // -ErrorAction guard makes a missing process yield empty stdout, which the
    // parse check below rejects, so the caller falls through to respawn.
    { encoding: 'utf8', windowsHide: true, stdio: ['ignore', 'pipe', 'ignore'] },
  ).trim();
  const seconds = Number.parseInt(output, 10);
  if (!Number.isSafeInteger(seconds) || seconds <= 0) {
    throw new Error(`could not parse process creation time for pid ${pid}: ${output}`);
  }
  return seconds;
}

export function validateHostInfo(hi: HostInfo): void {
  const expectedPipe = controlPipeName();
  if (hi.pipeName !== expectedPipe) {
    throw new Error('untrusted host.json: unexpected pipe name');
  }
  if (hi.version !== PROTO_VERSION) {
    throw new Error(`session-host protocol mismatch: host=${hi.version} client=${PROTO_VERSION}`);
  }
  if (!isLowerHexBytes(hi.nonce, 32)) {
    throw new Error('untrusted host.json: invalid nonce');
  }
  if (!Number.isSafeInteger(hi.pid) || hi.pid <= 0 || !Number.isSafeInteger(hi.createdUnix) || hi.createdUnix <= 0) {
    throw new Error('untrusted host.json: invalid pid or creation time');
  }
  let got: number;
  try {
    got = processCreationUnix(hi.pid);
  } catch (err) {
    const message = err instanceof Error ? err.message : String(err);
    throw new Error(`untrusted host.json: process creation check failed: ${message}`, { cause: err });
  }
  if (Math.abs(got - hi.createdUnix) > 1) {
    throw new Error('untrusted host.json: pid/creation mismatch');
  }
}

export function tryLoadValidHostInfo(): HostInfo | null {
  if (!existsSync(hostInfoPath())) {
    return null;
  }
  try {
    const hi = readHostInfo();
    validateHostInfo(hi);
    return hi;
  } catch {
    // A corrupt, stale (dead pid), or version-mismatched host.json means there is
    // no usable daemon to reuse. Return null — matching this function's contract
    // and waitForValidHostInfo's `if (hi)` expectation — so ensureHost falls
    // through to (re)spawn a fresh daemon, or, if a live pipe exists, refuse
    // safely. A force-killed daemon (e.g. dev-worktree.ps1) leaves a stale
    // host.json behind, which must not brick the next launch by throwing here.
    return null;
  }
}

export function randomClientNonce(): string {
  return crypto.randomBytes(32).toString('hex');
}

function hostNonceProof(
  hi: HostInfo,
  clientNonce: string,
  hostVersion: number,
  hostPid: number,
  hostCreatedUnix: number,
): string {
  if (!isLowerHexBytes(clientNonce, 32)) {
    throw new Error('invalid hello challenge');
  }
  const hmac = crypto.createHmac('sha256', Buffer.from(hi.nonce, 'hex'));
  hmac.update(`${HELLO_PROOF_PREFIX}${clientNonce}\n${hostVersion}\n${hostPid}\n${hostCreatedUnix}\n${hi.pipeName}`, 'utf8');
  return hmac.digest('hex');
}

export function verifyAuthenticatedHello(hi: HostInfo, clientNonce: string, response: Response): void {
  if (!response.ok) {
    throw new Error(`Hello failed: ${response.error || 'unknown error'}`);
  }
  const hostVersion = response.hostVersion;
  const hostPid = response.hostPid;
  const hostCreatedUnix = response.hostCreatedUnix;
  if (hostVersion !== PROTO_VERSION) {
    throw new Error(`session-host protocol mismatch: host=${hostVersion ?? 0} client=${PROTO_VERSION}`);
  }
  if (hostPid !== hi.pid || hostCreatedUnix !== hi.createdUnix) {
    throw new Error('session-host identity mismatch');
  }
  if (!response.hostNonceProof || !isLowerHexBytes(response.hostNonceProof, 32)) {
    throw new Error('session-host authentication failed');
  }
  const want = hostNonceProof(hi, clientNonce, hostVersion, hostPid, hostCreatedUnix);
  const gotBuf = Buffer.from(response.hostNonceProof, 'utf8');
  const wantBuf = Buffer.from(want, 'utf8');
  if (gotBuf.length !== wantBuf.length || !crypto.timingSafeEqual(gotBuf, wantBuf)) {
    throw new Error('session-host authentication failed');
  }
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

async function pipeConnectable(pipeName: string, timeoutMs: number): Promise<boolean> {
  try {
    const socket = await connectPipe(pipeName, timeoutMs);
    socket.end();
    return true;
  } catch {
    return false;
  }
}

async function waitForValidHostInfo(timeoutMs: number): Promise<HostInfo | null> {
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    await delay(150);
    const hi = tryLoadValidHostInfo();
    if (hi) {
      return hi;
    }
  }
  return null;
}

export async function ensureHost(csExe: string, opts?: { verbose?: boolean }): Promise<HostInfo> {
  const start = Date.now();
  const pipeName = controlPipeName();
  log.info('ensureHost start', { pipeName, verbose: !!opts?.verbose });
  const hi = tryLoadValidHostInfo();
  if (hi) {
    log.info('ensureHost reuse', { pipeName, pid: hi.pid, elapsedMs: Date.now() - start });
    return hi;
  }

  if (await pipeConnectable(pipeName, 800)) {
    log.info('ensureHost pipe connectable; waiting for host.json', { pipeName });
    const published = await waitForValidHostInfo(2_000);
    if (published) {
      log.info('ensureHost adopted published host', { pipeName, pid: published.pid, elapsedMs: Date.now() - start });
      return published;
    }
    throw new Error('unauthenticated session-host pipe exists; refusing to connect without valid host.json');
  }

  const child = spawn(csExe, ['session-host'], {
    detached: true,
    stdio: 'ignore',
    windowsHide: true,
    // HANGAR_DEBUG only affects a newly spawned daemon; a running daemon is unaffected until it restarts.
    env: { ...process.env, ...(opts?.verbose ? { HANGAR_DEBUG: '1' } : {}) },
  });
  log.info('ensureHost spawned session-host', { pipeName, pid: child.pid, verbose: !!opts?.verbose });
  child.unref();

  const published = await waitForValidHostInfo(10_000);
  if (published) {
    log.info('ensureHost spawn ready', { pipeName, pid: published.pid, elapsedMs: Date.now() - start });
    return published;
  }

  log.error('ensureHost spawn failed', { pipeName, elapsedMs: Date.now() - start });
  throw new Error('session-host did not publish a valid host.json');
}

export async function connectAttachStream(
  attachPipe: string,
  attachToken: string,
): Promise<net.Socket> {
  const start = Date.now();
  log.info('connectAttachStream start', { pipeName: attachPipe });
  try {
    const socket = await connectPipe(attachPipe);
    socket.write(frame(Buffer.from(attachToken, 'utf8')));
    log.info('connectAttachStream connected', { pipeName: attachPipe, elapsedMs: Date.now() - start });
    return socket;
  } catch (error) {
    log.error('connectAttachStream failed', {
      pipeName: attachPipe,
      elapsedMs: Date.now() - start,
      error: error instanceof Error ? error.message : String(error),
    });
    throw error;
  }
}

function delay(ms: number): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, ms));
}
