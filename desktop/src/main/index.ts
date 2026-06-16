import { app, BrowserWindow, dialog, ipcMain, shell } from 'electron';
import path from 'node:path';
import os from 'node:os';
import { readFileSync } from 'node:fs';
import type net from 'node:net';
import {
  ControlClient,
  PROTO_VERSION,
  Request,
  Response,
  connectAttachStream,
  connectPipe,
  ensureHost,
} from './host-client';

const CS_EXE =
  process.env.CS_EXE || path.resolve(app.getAppPath(), '..', 'dist', 'cs.exe');
const DEFAULT_COLS = 120;
const DEFAULT_ROWS = 30;

let mainWindow: BrowserWindow | null = null;
let control: ControlClient | null = null;
let attachSocket: net.Socket | null = null;
let activeSession: string | null = null;
let setupPromise: Promise<ControlClient> | null = null;

async function getControlClient(): Promise<ControlClient> {
  if (control) {
    return control;
  }
  if (!setupPromise) {
    setupPromise = (async () => {
      const pipeName = await ensureHost(CS_EXE);
      const client = new ControlClient(await connectPipe(pipeName));
      const hello = await client.call({ method: 'Hello', clientVersion: PROTO_VERSION });
      console.log('Hello ->', hello.hostVersion, hello.ok);
      if (!hello.ok) {
        throw new Error(`Hello failed: ${hello.error || 'unknown error'}`);
      }
      control = client;
      sendToRenderer('cs:ready', { hostVersion: hello.hostVersion, ok: hello.ok });
      return client;
    })();
  }
  return setupPromise;
}

// attachWorkspace attaches the renderer's terminal to a workspace's agent
// session (by session name). Only one workspace is attached at a time; switching
// detaches the previous one. Detaching never kills the session — workspaces live
// in the daemon and persist across UI restarts.
async function attachWorkspace(
  sessionName: string,
  cols = DEFAULT_COLS,
  rows = DEFAULT_ROWS,
): Promise<Response> {
  const client = await getControlClient();
  detachWorkspace();

  const attached = await client.call({ method: 'Attach', session: sessionName, cols, rows });
  if (!attached.ok || !attached.attachPipe || !attached.attachToken) {
    throw new Error(`Attach: ${attached.error || 'missing attach pipe/token'}`);
  }

  activeSession = sessionName;
  attachSocket = await connectAttachStream(attached.attachPipe, attached.attachToken);
  attachSocket.on('data', (chunk) => sendToRenderer('term:data', new Uint8Array(chunk)));
  attachSocket.on('close', () => sendToRenderer('term:closed'));
  attachSocket.on('error', (error) => sendToRenderer('term:error', error.message));

  sendToRenderer('term:ready', { session: sessionName });
  return attached;
}

function detachWorkspace(): void {
  if (attachSocket) {
    attachSocket.destroy();
    attachSocket = null;
  }
  activeSession = null;
}

function createWindow(): void {
  mainWindow = new BrowserWindow({
    width: 1320,
    height: 820,
    minWidth: 1080,
    minHeight: 680,
    backgroundColor: '#1e1e1e',
    webPreferences: {
      preload: path.join(__dirname, '..\\preload\\index.js'),
      contextIsolation: true,
      nodeIntegration: false,
      sandbox: false,
    },
  });

  if (process.env.ELECTRON_RENDERER_URL) {
    mainWindow.loadURL(process.env.ELECTRON_RENDERER_URL);
  } else {
    mainWindow.loadFile(path.join(__dirname, '..\\renderer\\index.html'));
  }

  mainWindow.webContents.once('did-finish-load', () => {
    getControlClient().catch((error) => {
      console.error('setup error:', error);
      sendToRenderer('term:error', String(error.message || error));
    });
  });
}

function sendToRenderer(channel: string, payload?: unknown): void {
  if (mainWindow && !mainWindow.isDestroyed()) {
    mainWindow.webContents.send(channel, payload);
  }
}

ipcMain.handle('cs:call', async (_event, request: Omit<Request, 'id'>) => {
  const client = await getControlClient();
  return client.call(request);
});

ipcMain.handle(
  'cs:attach-workspace',
  async (_event, args: { sessionName: string; cols?: number; rows?: number }) => {
    return attachWorkspace(args.sessionName, args.cols ?? DEFAULT_COLS, args.rows ?? DEFAULT_ROWS);
  },
);

ipcMain.handle('cs:detach', async () => {
  detachWorkspace();
});

ipcMain.handle('cs:pick-folder', async (): Promise<string | null> => {
  if (!mainWindow) return null;
  const result = await dialog.showOpenDialog(mainWindow, {
    title: 'Select a git repository',
    properties: ['openDirectory'],
  });
  if (result.canceled || result.filePaths.length === 0) return null;
  return result.filePaths[0];
});

// Returns the daemon's default agent program (from ~/.claude-squad/config.json)
// so the create form can pre-fill a known-good agent instead of submitting a
// blank field that silently falls back to whatever the config holds.
ipcMain.handle('cs:get-default-program', async (): Promise<string> => {
  try {
    const cfgPath = path.join(os.homedir(), '.claude-squad', 'config.json');
    const cfg = JSON.parse(readFileSync(cfgPath, 'utf8')) as { default_program?: string };
    const prog = (cfg.default_program || '').trim();
    if (prog) return prog;
  } catch {
    // No config yet (or unreadable) — fall through to the built-in default.
  }
  return 'copilot';
});

// Open a detected preview URL in the user's default browser. Restricted to
// http/https so a malicious run-output URL can't launch arbitrary schemes.
ipcMain.handle('cs:open-external', async (_event, url: string): Promise<void> => {
  try {
    const parsed = new URL(url);
    if (parsed.protocol === 'http:' || parsed.protocol === 'https:') {
      await shell.openExternal(parsed.toString());
    }
  } catch {
    // Ignore malformed URLs.
  }
});

ipcMain.on('term:input', (_event, data: string) => {
  if (attachSocket) {
    attachSocket.write(Buffer.from(data, 'utf8'));
  }
});

ipcMain.on('term:resize', (_event, { cols, rows }: { cols: number; rows: number }) => {
  if (control && activeSession && Number.isFinite(cols) && Number.isFinite(rows)) {
    control.call({ method: 'Resize', session: activeSession, cols, rows }).catch(() => {});
  }
});

app.whenReady().then(createWindow);

app.on('before-quit', () => {
  detachWorkspace();
});

app.on('window-all-closed', () => {
  // Detach only — workspaces live in the daemon and persist across UI restarts.
  detachWorkspace();
  if (control) {
    control.close();
    control = null;
  }
  app.quit();
});
