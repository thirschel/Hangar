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
  randomClientNonce,
  verifyAuthenticatedHello,
  type DirEntry,
  type FileContents,
} from './host-client';
import {
  getSettings,
  applySettings,
  detectShells,
  isFirstRun,
  markSetupComplete,
  type Settings,
  type ShellProfile,
} from './settings';
import { createTray, destroyTray } from './tray';
import { buildAsset } from './assets';
import { log } from './logger';
import {
  checkForUpdate,
  downloadUpdate,
  getUpdateStatus,
  initAutoUpdate,
  installUpdate,
  type UpdateStatus,
} from './updater';

export type AppInfo = {
  version: string;
  appName: string;
  electronVersion: string;
  nodeVersion: string;
  platform: string;
  arch: string;
  githubUrl: string;
  author: string;
};

const CS_EXE =
  process.env.CS_EXE ||
  (app.isPackaged
    ? path.join(process.resourcesPath, 'dist', 'cs.exe')
    : path.resolve(app.getAppPath(), '..', 'dist', 'cs.exe'));
const DEFAULT_COLS = 120;
const DEFAULT_ROWS = 30;

let mainWindow: BrowserWindow | null = null;
let control: ControlClient | null = null;
let authenticatedHello: Response | null = null;
// Live attach streams keyed by session name, so the agent and an in-worktree
// shell terminal can stream concurrently. Bounded in practice to the selected
// workspace's agent (+ its shell), which are swapped when the selection changes.
const attachments = new Map<string, net.Socket>();
// Shell sessions (sh_<workspaceId>) this app run created, for cleanup on archive
// and quit so no orphan PowerShell is left in the daemon.
const shellSessions = new Set<string>();
const shellSessionPrograms = new Map<string, string>();
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
  authenticatedHello = null;
  if (!setupPromise) {
    setupPromise = (async () => {
      try {
        const hostInfo = await ensureHost(CS_EXE);
        const client = new ControlClient(await connectPipe(hostInfo.pipeName));
        try {
          const clientNonce = randomClientNonce();
          const hello = await client.call({ method: 'Hello', clientVersion: PROTO_VERSION, clientNonce });
          verifyAuthenticatedHello(hostInfo, clientNonce, hello);
          log.info('Hello ->', hello.hostVersion, hello.ok);
          control = client;
          authenticatedHello = hello;
          sendToRenderer('cs:ready', { hostVersion: hello.hostVersion, ok: hello.ok });
          return client;
        } catch (error) {
          client.close();
          throw error;
        }
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

function quoteProgramPart(part: string): string {
  return /\s/.test(part) ? `"${part.replace(/"/g, '\\"')}"` : part;
}

function profileProgram(profile: ShellProfile): string {
  return [profile.command, ...(profile.args ?? [])].map(quoteProgramPart).join(' ');
}

function defaultShellProgram(): string {
  const settings = getSettings();
  const profiles = settings.terminalProfiles ?? [];
  const profile =
    profiles.find((candidate) => candidate.id === settings.defaultTerminalProfileId) ??
    profiles[0];
  return profile ? profileProgram(profile) : 'cmd.exe';
}

function createWindow(): void {
  mainWindow = new BrowserWindow({
    width: 1320,
    height: 820,
    minWidth: 1080,
    minHeight: 680,
    icon: buildAsset('icon.ico'),
    backgroundColor: '#1e1e1e',
    webPreferences: {
      preload: path.join(__dirname, '..\\preload\\index.js'),
      contextIsolation: true,
      nodeIntegration: false,
      sandbox: false,
      // Let the renderer play the notification chime without a prior user
      // gesture, and keep it responsive while hidden/minimized to the tray so
      // the sound fires even when the window isn't focused.
      autoplayPolicy: 'no-user-gesture-required',
      backgroundThrottling: false,
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
    if (isFirstRun()) {
      sendToRenderer('cs:first-run');
    }
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
    if (request.method === 'Hello' && authenticatedHello) {
      return authenticatedHello;
    }
    return await client.call(request);
  } catch {
    // If the pipe dropped (daemon restarted), reconnect and retry once.
    if (control?.isClosed()) {
      control = null;
      authenticatedHello = null;
    }
    const client = await getControlClient();
    if (request.method === 'Hello' && authenticatedHello) {
      return authenticatedHello;
    }
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

ipcMain.handle(
  'cs:get-history',
  async (
    _event,
    args: { session: string; includeScreen?: boolean; cols?: number; rows?: number },
  ): Promise<{ ansi: string; altScreen: boolean; scrollbackLines: number }> => {
    const client = await getControlClient();
    const r = await client.call({
      method: 'CaptureHistory',
      session: args.session,
      includeScreen: args.includeScreen ?? false,
      cols: args.cols,
      rows: args.rows,
    });
    if (!r.ok) {
      // A workspace session that isn't live yet (e.g. just after a daemon
      // restart, before attach revives it) has no scrollback to prime. Treat
      // it as empty history rather than throwing, so Electron doesn't log a
      // handler rejection for this best-effort call; attach revives the session
      // and streams live output a moment later.
      if ((r.error ?? '').includes('no such session')) {
        return { ansi: '', altScreen: false, scrollbackLines: 0 };
      }
      throw new Error(r.error || 'CaptureHistory failed');
    }
    return {
      ansi: r.content ?? '',
      altScreen: r.altScreen ?? false,
      scrollbackLines: r.scrollbackLines ?? 0,
    };
  },
);

// Ensure a shell session running in the workspace's worktree exists, and
// return its session name. Lazily created on first Terminal-tab open; kept alive
// in the daemon so re-opening re-attaches the same shell (cwd/history preserved).
ipcMain.handle(
  'cs:ensure-shell',
  async (
    _event,
    args: { workspaceId: string; worktreePath: string; cols?: number; rows?: number; program?: string },
  ): Promise<string> => {
    const session = `sh_${args.workspaceId}`;
    const program = args.program?.trim() || defaultShellProgram();
    const client = await getControlClient();
    const has = await client.call({ method: 'HasSession', session });
    const tracked = shellSessionPrograms.get(session);
    // Only respawn when we KNOW the running shell differs from the requested one.
    // An untracked-but-alive session (e.g. reattaching to a daemon-persisted shell
    // after an app restart) is left intact so its history/processes survive.
    const programChanged = tracked !== undefined && tracked !== program;
    if (has.ok && has.exists && programChanged) {
      detachSession(session);
      const killed = await client.call({ method: 'KillSession', session });
      if (!killed.ok) throw new Error(killed.error || 'failed to restart shell');
    }
    if (!has.ok || !has.exists || programChanged) {
      const created = await client.call({
        method: 'CreateSession',
        session,
        program,
        workDir: args.worktreePath,
        cols: args.cols ?? DEFAULT_COLS,
        rows: args.rows ?? DEFAULT_ROWS,
      });
      if (!created.ok) throw new Error(created.error || 'failed to start shell');
      shellSessionPrograms.set(session, program);
    } else if (tracked === undefined) {
      // Reattached to a pre-existing session; record its assumed program so a
      // later explicit shell switch still triggers a respawn.
      shellSessionPrograms.set(session, program);
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
  shellSessionPrograms.delete(session);
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

ipcMain.handle('cs:detect-shells', async (): Promise<ShellProfile[]> => detectShells());

ipcMain.handle('cs:complete-setup', async (_event, opts: { autoUpdate: boolean }): Promise<void> => {
  applySettings({ autoUpdate: opts.autoUpdate });
  markSetupComplete();
});

ipcMain.handle(
  'cs:get-app-info',
  async (): Promise<AppInfo> => ({
    version: app.getVersion(),
    appName: app.getName(),
    electronVersion: process.versions.electron,
    nodeVersion: process.versions.node,
    platform: process.platform,
    arch: process.arch,
    githubUrl: 'https://github.com/thirschel/Hangar',
    author: 'Hangar contributors',
  }),
);

ipcMain.handle('cs:get-update-status', async (): Promise<UpdateStatus> => getUpdateStatus());

ipcMain.handle('cs:check-for-update', async () => {
  return checkForUpdate();
});

ipcMain.handle('cs:download-update', async () => {
  return downloadUpdate();
});

ipcMain.handle('cs:install-update', async () => {
  installUpdate();
});

// Show a native OS notification (e.g. agent finished / needs input). Clicking it
// reveals the window and asks the renderer to select the originating workspace.
ipcMain.handle(
  'cs:notify',
  async (_event, n: { title: string; body: string; workspaceId?: string }): Promise<void> => {
    const settings = getSettings();
    if (!settings.notifications || !Notification.isSupported()) return;
    // Suppress the OS default chime: when the user enabled the in-app sound we
    // play our own (below), and when they muted it there should be no sound at
    // all. Either way the native ding would double up, so silence it here.
    const notification = new Notification({
      title: n.title,
      body: n.body,
      icon: buildAsset('icon.png'),
      silent: true,
    });
    notification.on('click', () => {
      if (mainWindow) {
        if (mainWindow.isMinimized()) mainWindow.restore();
        mainWindow.show();
        mainWindow.focus();
      }
      if (n.workspaceId) sendToRenderer('cs:focus-workspace', n.workspaceId);
    });
    notification.show();
    if (settings.notificationSound) sendToRenderer('cs:play-notification-sound');
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
  // Windows taskbar + toast identity. Without an explicit AppUserModelId the OS
  // attributes the app (and its taskbar icon/notifications) to the generic
  // electron.exe rather than to Hangar. Dev runs (which execute under the stock
  // electron.exe, named "Electron" with the default icon) use a distinct
  // ".dev" id so they can't register a stale "Electron" Start-menu shortcut
  // under the packaged app's id and shadow its real taskbar icon/name.
  if (process.platform === 'win32') {
    app.setAppUserModelId(app.isPackaged ? 'com.thirschel.hangar' : 'com.thirschel.hangar.dev');
  }
  // Hide the default application menu bar (File / Edit / View / Window / Help).
  Menu.setApplicationMenu(null);
  createWindow();
  createTray(() => mainWindow);
  initAutoUpdate(mainWindow);
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
