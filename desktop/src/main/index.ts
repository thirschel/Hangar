import { app, BrowserWindow, dialog, globalShortcut, ipcMain, Menu, Notification, shell } from 'electron';
import path from 'node:path';
import os from 'node:os';
import { readFileSync, readdirSync, statSync } from 'node:fs';
import type net from 'node:net';
import {
  ControlClient,
  PROTO_VERSION,
  Request,
  Response,
  connectAttachStream,
  connectPipe,
  ensureHost,
  type DirEntry,
  type FileContents,
} from './host-client';
import { getSettings, applySettings, type Settings } from './settings';
import { createTray, destroyTray } from './tray';
import { buildAsset } from './assets';
import { log } from './logger';
import { initAutoUpdate } from './updater';

const CS_EXE =
  process.env.CS_EXE ||
  (app.isPackaged
    ? path.join(process.resourcesPath, 'dist', 'cs.exe')
    : path.resolve(app.getAppPath(), '..', 'dist', 'cs.exe'));
const DEFAULT_COLS = 120;
const DEFAULT_ROWS = 30;

let mainWindow: BrowserWindow | null = null;
let control: ControlClient | null = null;
// Live attach streams keyed by session name, so the agent and an in-worktree
// shell terminal can stream concurrently. Bounded in practice to the selected
// workspace's agent (+ its shell), which are swapped when the selection changes.
const attachments = new Map<string, net.Socket>();
// Shell sessions (sh_<workspaceId>) this app run created, for cleanup on archive
// and quit so no orphan PowerShell is left in the daemon.
const shellSessions = new Set<string>();
let setupPromise: Promise<ControlClient> | null = null;
let isQuitting = false;

process.on('uncaughtException', (err) => log.error('uncaughtException', err));
process.on('unhandledRejection', (reason) => log.error('unhandledRejection', reason));

async function getControlClient(): Promise<ControlClient> {
  if (control && !control.isClosed()) {
    return control;
  }
  // Drop a dead client so a daemon restart triggers a fresh connect.
  control = null;
  if (!setupPromise) {
    setupPromise = (async () => {
      try {
        const pipeName = await ensureHost(CS_EXE);
        const client = new ControlClient(await connectPipe(pipeName));
        const hello = await client.call({ method: 'Hello', clientVersion: PROTO_VERSION });
        log.info('Hello ->', hello.hostVersion, hello.ok);
        if (!hello.ok) {
          throw new Error(`Hello failed: ${hello.error || 'unknown error'}`);
        }
        control = client;
        sendToRenderer('cs:ready', { hostVersion: hello.hostVersion, ok: hello.ok });
        return client;
      } finally {
        // Always clear the in-flight promise so a future reconnect can re-run setup.
        setupPromise = null;
      }
    })();
  }
  return setupPromise;
}

// attachSession opens a live stream to a daemon session (agent or shell) by name.
// Streams are keyed by session so multiple can run at once; data/close/error are
// reported to the renderer tagged with the session name. Re-attaching an already
// open session is a no-op.
async function attachSession(
  sessionName: string,
  cols = DEFAULT_COLS,
  rows = DEFAULT_ROWS,
): Promise<Response> {
  const client = await getControlClient();
  if (attachments.has(sessionName)) {
    return { id: 0, ok: true };
  }

  const attached = await client.call({ method: 'Attach', session: sessionName, cols, rows });
  if (!attached.ok || !attached.attachPipe || !attached.attachToken) {
    throw new Error(`Attach: ${attached.error || 'missing attach pipe/token'}`);
  }

  const socket = await connectAttachStream(attached.attachPipe, attached.attachToken);
  attachments.set(sessionName, socket);
  socket.on('data', (chunk) => sendToRenderer('term:data', { session: sessionName, chunk: new Uint8Array(chunk) }));
  socket.on('close', () => {
    attachments.delete(sessionName);
    sendToRenderer('term:closed', { session: sessionName });
  });
  socket.on('error', (error) => sendToRenderer('term:error', { session: sessionName, message: error.message }));

  sendToRenderer('term:ready', { session: sessionName });
  return attached;
}

function detachSession(sessionName: string): void {
  const socket = attachments.get(sessionName);
  if (socket) {
    socket.destroy();
    attachments.delete(sessionName);
  }
}

function detachAll(): void {
  for (const socket of attachments.values()) socket.destroy();
  attachments.clear();
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

  // Closing the window minimizes to the tray (keeping workspaces + daemon live)
  // unless the user is really quitting or has disabled the behavior.
  mainWindow.on('close', (e) => {
    if (!isQuitting && getSettings().minimizeToTray) {
      e.preventDefault();
      mainWindow?.hide();
    }
  });

  if (process.env.ELECTRON_RENDERER_URL) {
    mainWindow.loadURL(process.env.ELECTRON_RENDERER_URL);
  } else {
    mainWindow.loadFile(path.join(__dirname, '..\\renderer\\index.html'));
  }

  mainWindow.webContents.once('did-finish-load', () => {
    getControlClient().catch((error) => {
      log.error('setup error:', error);
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
  try {
    const client = await getControlClient();
    return await client.call(request);
  } catch {
    // If the pipe dropped (daemon restarted), reconnect and retry once.
    if (control?.isClosed()) control = null;
    const client = await getControlClient();
    return await client.call(request);
  }
});

ipcMain.handle(
  'cs:attach-session',
  async (_event, args: { sessionName: string; cols?: number; rows?: number }) => {
    return attachSession(args.sessionName, args.cols ?? DEFAULT_COLS, args.rows ?? DEFAULT_ROWS);
  },
);

ipcMain.handle('cs:detach-session', async (_event, sessionName: string) => {
  detachSession(sessionName);
});

// Ensure a PowerShell session running in the workspace's worktree exists, and
// return its session name. Lazily created on first Terminal-tab open; kept alive
// in the daemon so re-opening re-attaches the same shell (cwd/history preserved).
ipcMain.handle(
  'cs:ensure-shell',
  async (_event, args: { workspaceId: string; worktreePath: string; cols?: number; rows?: number }): Promise<string> => {
    const session = `sh_${args.workspaceId}`;
    const client = await getControlClient();
    const has = await client.call({ method: 'HasSession', session });
    if (!has.ok || !has.exists) {
      const created = await client.call({
        method: 'CreateSession',
        session,
        program: 'powershell.exe -NoLogo',
        workDir: args.worktreePath,
        cols: args.cols ?? DEFAULT_COLS,
        rows: args.rows ?? DEFAULT_ROWS,
      });
      if (!created.ok) throw new Error(created.error || 'failed to start shell');
    }
    shellSessions.add(session);
    return session;
  },
);

// Kill a workspace's shell session (on archive). Detaches first.
ipcMain.handle('cs:close-shell', async (_event, workspaceId: string): Promise<void> => {
  const session = `sh_${workspaceId}`;
  detachSession(session);
  shellSessions.delete(session);
  try {
    const client = await getControlClient();
    await client.call({ method: 'KillSession', session });
  } catch {
    // Daemon may be gone already; nothing to clean up.
  }
});

// Files tab: resolve a path strictly inside the worktree (reject traversal).
function resolveInWorktree(worktreePath: string, rel: string): string {
  const root = path.resolve(worktreePath);
  const target = path.resolve(root, rel || '.');
  if (target !== root && !target.startsWith(root + path.sep)) {
    throw new Error('path is outside the worktree');
  }
  return target;
}

ipcMain.handle('cs:fs-list', async (_event, args: { worktreePath: string; relDir: string }): Promise<DirEntry[]> => {
  const dir = resolveInWorktree(args.worktreePath, args.relDir);
  const entries = readdirSync(dir, { withFileTypes: true });
  return entries
    .filter((e) => e.name !== '.git')
    .map((e) => ({ name: e.name, dir: e.isDirectory() }))
    .sort((a, b) => (a.dir === b.dir ? a.name.localeCompare(b.name) : a.dir ? -1 : 1));
});

ipcMain.handle('cs:fs-read', async (_event, args: { worktreePath: string; relFile: string }): Promise<FileContents> => {
  try {
    const file = resolveInWorktree(args.worktreePath, args.relFile);
    const st = statSync(file);
    if (st.size > 1_000_000) return { kind: 'tooLarge', size: st.size };
    const buf = readFileSync(file);
    if (buf.subarray(0, 8192).includes(0)) return { kind: 'binary' };
    return { kind: 'text', text: buf.toString('utf8') };
  } catch (e) {
    return { kind: 'error', message: e instanceof Error ? e.message : String(e) };
  }
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

// Returns the daemon's default agent program (from ~/.hangar/config.json)
// so the create form can pre-fill a known-good agent instead of submitting a
// blank field that silently falls back to whatever the config holds.
ipcMain.handle('cs:get-default-program', async (): Promise<string> => {
  try {
    const cfgPath = path.join(os.homedir(), '.hangar', 'config.json');
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

ipcMain.handle('cs:get-settings', async (): Promise<Settings> => getSettings());

ipcMain.handle('cs:set-settings', async (_event, patch: Partial<Settings>): Promise<Settings> => {
  return applySettings(patch);
});

// Show a native OS notification (e.g. agent finished / needs input). Clicking it
// reveals the window and asks the renderer to select the originating workspace.
ipcMain.handle(
  'cs:notify',
  async (_event, n: { title: string; body: string; workspaceId?: string }): Promise<void> => {
    if (!getSettings().notifications || !Notification.isSupported()) return;
    const notification = new Notification({ title: n.title, body: n.body, icon: buildAsset('icon.png') });
    notification.on('click', () => {
      if (mainWindow) {
        if (mainWindow.isMinimized()) mainWindow.restore();
        mainWindow.show();
        mainWindow.focus();
      }
      if (n.workspaceId) sendToRenderer('cs:focus-workspace', n.workspaceId);
    });
    notification.show();
  },
);

// Badge count: show the number of workspaces awaiting input on the taskbar icon.
ipcMain.handle('cs:set-badge', async (_event, count: number): Promise<void> => {
  app.setBadgeCount(count);
});

ipcMain.on('term:input', (_event, args: { session: string; data: string }) => {
  const socket = attachments.get(args.session);
  if (socket) {
    socket.write(Buffer.from(args.data, 'utf8'));
  }
});

ipcMain.on('term:resize', (_event, args: { session: string; cols: number; rows: number }) => {
  if (control && args.session && Number.isFinite(args.cols) && Number.isFinite(args.rows)) {
    control.call({ method: 'Resize', session: args.session, cols: args.cols, rows: args.rows }).catch(() => {});
  }
});

app.whenReady().then(() => {
  // Hide the default application menu bar (File / Edit / View / Window / Help).
  Menu.setApplicationMenu(null);
  createWindow();
  createTray(() => mainWindow);
  initAutoUpdate();
  // Global hotkey: summon/focus the app window from anywhere.
  globalShortcut.register('CommandOrControl+Shift+Space', () => {
    if (!mainWindow) return;
    if (mainWindow.isMinimized()) mainWindow.restore();
    if (mainWindow.isVisible()) mainWindow.focus();
    else mainWindow.show();
  });
});

app.on('before-quit', () => {
  isQuitting = true;
  globalShortcut.unregisterAll();
  destroyTray();
  // Best-effort: kill scratch shell sessions so no orphan PowerShell lingers.
  if (control && !control.isClosed()) {
    for (const session of shellSessions) {
      control.call({ method: 'KillSession', session }).catch(() => {});
    }
  }
  detachAll();
});

app.on('window-all-closed', () => {
  // Detach only — workspaces live in the daemon and persist across UI restarts.
  detachAll();
  if (control) {
    control.close();
    control = null;
  }
  app.quit();
});
