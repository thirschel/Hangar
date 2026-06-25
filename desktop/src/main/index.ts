import { app, BrowserWindow, dialog, globalShortcut, ipcMain, Menu, Notification, shell } from 'electron';
import path from 'node:path';
import os from 'node:os';
import { readFileSync, readdirSync, statSync } from 'node:fs';
import { open as openFile, stat as statFile } from 'node:fs/promises';
import type net from 'node:net';
import {
  ControlClient,
  PROTO_VERSION,
  Request,
  Response,
  connectAttachStream,
  connectEventStream,
  connectPipe,
  ensureHost,
  killHostProcess,
  randomClientNonce,
  requestHostShutdown,
  verifyAuthenticatedHello,
  type DirEntry,
  type EventFrame,
  type FileContents,
  type ModelInfo,
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
import { isSoftwareCompositing, mergeDisableFeatures, isRemoteSession } from './render-detect';
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
  // True when Chromium is compositing in software (e.g. RDP/VDI, no GPU). The
  // renderer uses this to force terminal repaints that the software compositor
  // otherwise drops. See render-detect.ts / isSoftwareCompositing.
  softwareCompositing: boolean;
};

const CS_EXE =
  process.env.CS_EXE ||
  (app.isPackaged
    ? path.join(process.resourcesPath, 'dist', 'cs.exe')
    : path.resolve(app.getAppPath(), '..', 'dist', 'cs.exe'));
const DEFAULT_COLS = 120;
const DEFAULT_ROWS = 30;
const DEFAULT_LOG_BYTES = 65_536;
type LogWhich = 'host' | 'desktop' | 'hangar';
type LogPaths = { hostLog: string; desktopLog: string; hangarLog: string };
type ReadLogResult = { path: string; content: string; truncated: boolean };

// Optionally disable GPU/hardware acceleration BEFORE the app is ready (the API
// has no effect once Electron has initialized the GPU). On some locked-down
// corporate machines (RDP/VDI, GPU disabled by policy, or buggy drivers) the
// React UI paints but xterm's terminal layer renders blank; disabling hardware
// acceleration is the standard remedy. Persisted as a setting; takes effect on
// the next launch. Read synchronously and guarded so startup never fails here.
const hardwareAccelerationDisabled = ((): boolean => {
  try {
    if (getSettings().disableHardwareAcceleration) {
      app.disableHardwareAcceleration();
      return true;
    }
  } catch {
    // Settings unreadable at startup; keep hardware acceleration on.
  }
  return false;
})();

// Disable Chromium's native-window occlusion detection BEFORE the app is ready
// (the switch has no effect afterward). Over RDP, Chromium's occlusion tracker
// frequently false-positives the window as occluded and pauses the software
// compositor — leaving the terminal blank until a resize. VS Code ships this
// switch by default; we apply it unconditionally (merge-safely so it never
// clobbers another --disable-features value). See docs/rdp-blank-terminal-postmortem.md.
const windowOcclusionDisabled = ((): boolean => {
  try {
    const merged = mergeDisableFeatures(app.commandLine.getSwitchValue('disable-features'), [
      'CalculateNativeWinOcclusion',
    ]);
    app.commandLine.appendSwitch('disable-features', merged);
    // Companion "occlusion set": stop Chromium throttling/backgrounding a window
    // it believes is occluded/hidden over RDP.
    app.commandLine.appendSwitch('disable-backgrounding-occluded-windows');
    app.commandLine.appendSwitch('disable-renderer-backgrounding');
    return true;
  } catch {
    // Command line unavailable this early; leave native occlusion detection enabled.
    return false;
  }
})();

// Disable Chromium's DirectComposition present path BEFORE the app is ready. Over
// RDP, DirectComposition/MPO is the most common reason content is composited but
// never PRESENTED to the screen. Disabling it routes presents through a path the
// RDP stack reliably blits. ONLY applied in detected remote sessions so local GPU
// machines (where DirectComposition is the efficient path) are unaffected. See
// docs/rdp-blank-terminal-postmortem.md.
const directCompositionDisabled = ((): boolean => {
  try {
    if (isRemoteSession()) {
      app.commandLine.appendSwitch('disable-direct-composition');
      return true;
    }
  } catch {
    // Settings unreadable at startup; leave DirectComposition enabled.
  }
  return false;
})();

function hostVerbose(): boolean {
  return getSettings().verboseLogging || !!process.env.HANGAR_DEBUG;
}

// isSoftwareCompositing is defined in ./render-detect (electron-free + tested).
// softwareCompositing is resolved once the app is ready (getGPUFeatureStatus is
// only meaningful then) and gates the terminal renderer selection (canvas under
// software compositing) exposed to the renderer via cs:get-render-info.
let softwareCompositing = false;

// capturePixelProbe records a decision signal for the RDP blank-terminal bug: it
// captures the composited surface and logs ONLY non-background pixel counts + a
// cheap checksum (never screenshots or content). capturePage reads the COMPOSITED
// surface, not the on-screen presented pixels — so "pixels present in the capture
// but the screen is visually blank" is the native present/occlusion (H1)
// signature. When the renderer has reported the terminal pane's rect we capture
// ONLY that rect, so the non-background count is the TERMINAL's content alone
// (isolating it from the always-painting React UI): a near-zero terminal count
// means the DOM never rasterized (H2); a large count while the screen is blank
// means it rasterized but never presented (H1). Gated behind the
// terminalDiagnostics setting (capturePage has a cost) + one in-flight capture.
const terminalRects = new Map<string, { x: number; y: number; width: number; height: number }>();
let captureInFlight = false;
async function capturePixelProbe(tag: string, session?: string): Promise<void> {
  try {
    if (!getSettings().terminalDiagnostics) return;
  } catch {
    return;
  }
  const w = mainWindow;
  if (!w || w.isDestroyed() || captureInFlight) return;
  captureInFlight = true;
  try {
    const rect = session ? terminalRects.get(session) : undefined;
    const valid = rect && rect.width >= 1 && rect.height >= 1;
    const image = valid ? await w.webContents.capturePage(rect) : await w.webContents.capturePage();
    const size = image.getSize();
    const bitmap = image.toBitmap(); // BGRA, 4 bytes per pixel.
    let sampledNonBackground = 0;
    let checksum = 0;
    // Background is #1e1e1e. Sample every 4th pixel to stay cheap on a large area.
    for (let i = 0; i + 2 < bitmap.length; i += 16) {
      const b = bitmap[i];
      const g = bitmap[i + 1];
      const r = bitmap[i + 2];
      if (!(b === 0x1e && g === 0x1e && r === 0x1e)) {
        sampledNonBackground += 1;
        checksum = (checksum + r * 3 + g * 5 + b * 7) >>> 0;
      }
    }
    log.info('capturePixelProbe', {
      tag,
      region: valid ? 'terminal' : 'window',
      width: size.width,
      height: size.height,
      sampledNonBackgroundPixels: sampledNonBackground,
      checksum,
    });
  } catch (error) {
    log.error('capturePixelProbe failed', {
      tag,
      error: error instanceof Error ? error.message : String(error),
    });
  } finally {
    captureInFlight = false;
  }
}

function diagnosticLogPaths(): LogPaths {
  return {
    hostLog: path.join(os.homedir(), '.hangar', 'host.log'),
    desktopLog: log.file,
    hangarLog: path.join(os.tmpdir(), 'hangar.log'),
  };
}

function diagnosticLogPath(which: LogWhich): string {
  const paths = diagnosticLogPaths();
  switch (which) {
    case 'host':
      return paths.hostLog;
    case 'desktop':
      return paths.desktopLog;
    case 'hangar':
      return paths.hangarLog;
    default:
      throw new Error('unknown log file');
  }
}

function assertLogWhich(which: unknown): asserts which is LogWhich {
  if (which !== 'host' && which !== 'desktop' && which !== 'hangar') {
    throw new Error('unknown log file');
  }
}

function programNameOnly(program: string): string {
  const trimmed = program.trim();
  const match = trimmed.match(/^"([^"]+)"|^(\S+)/);
  return path.basename(match?.[1] || match?.[2] || trimmed || '');
}

function describeResponse(r: Response): Record<string, unknown> {
  return { ok: r.ok, error: r.error, alive: r.alive, exitCode: r.exitCode };
}

let mainWindow: BrowserWindow | null = null;
let control: ControlClient | null = null;
let authenticatedHello: Response | null = null;
// Live attach streams keyed by session name, so the agent and an in-worktree
// shell terminal can stream concurrently. Bounded in practice to the selected
// workspace's agent (+ its shell), which are swapped when the selection changes.
const attachments = new Map<string, net.Socket>();
const eventStreams = new Map<string, net.Socket>();
// Per-workspace shell program (sh_<workspaceId> -> program string), so an explicit
// shell switch can detect a changed program and respawn while a reattached,
// daemon-persisted shell is left intact.
const shellSessionPrograms = new Map<string, string>();
let setupPromise: Promise<ControlClient> | null = null;
let isQuitting = false;
// Set once the quit teardown (host shutdown + cleanup) has run, so the second
// `before-quit` pass — the one we re-issue after the async teardown — is allowed
// through instead of looping.
let quitTeardownDone = false;

// Single-instance lock. Hangar drives a single per-user session-host; a second
// app process must not spin up another window/daemon client. The primary instance
// holds the lock; a second launch fails to acquire it, surfaces the already-running
// window via the `second-instance` event, and quits. `whenReady` is also guarded so
// a losing instance never creates a window.
const isPrimaryInstance = app.requestSingleInstanceLock();
if (!isPrimaryInstance) {
  app.quit();
} else {
  app.on('second-instance', () => {
    revealMainWindow();
  });
}

// Reveal + focus the main window (restoring from minimized and un-hiding from the
// tray). Used when a second launch is attempted against the running instance.
function revealMainWindow(): void {
  if (!mainWindow) return;
  if (mainWindow.isMinimized()) mainWindow.restore();
  if (!mainWindow.isVisible()) mainWindow.show();
  mainWindow.focus();
}

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
        const hostInfo = await ensureHost(CS_EXE, { verbose: hostVerbose() });
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
  const start = Date.now();
  log.info('attachSession start', { session: sessionName, cols, rows });
  const client = await getControlClient();
  if (attachments.has(sessionName)) {
    log.info('attachSession already attached', { session: sessionName, elapsedMs: Date.now() - start });
    return { id: 0, ok: true };
  }

  const attached = await client.call({ method: 'Attach', session: sessionName, cols, rows });
  log.info('attachSession Attach result', { session: sessionName, ...describeResponse(attached) });
  if (!attached.ok || !attached.attachPipe || !attached.attachToken) {
    throw new Error(`Attach: ${attached.error || 'missing attach pipe/token'}`);
  }

  let socket: net.Socket;
  const connectStart = Date.now();
  try {
    socket = await connectAttachStream(attached.attachPipe, attached.attachToken);
    log.info('attachSession stream connected', { session: sessionName, elapsedMs: Date.now() - connectStart });
  } catch (error) {
    log.error('attachSession stream failed', {
      session: sessionName,
      elapsedMs: Date.now() - connectStart,
      error: error instanceof Error ? error.message : String(error),
    });
    throw error;
  }
  attachments.set(sessionName, socket);
  const dataStats = { total: 0, firstLogged: false };
  socket.on('data', (chunk) => {
    const bytes = typeof chunk === 'string' ? Buffer.from(chunk) : chunk;
    // Copy into a fresh, tightly-bound Uint8Array. `bytes` is a Node Buffer
    // backed by a SHARED internal pool; forwarding a view over `bytes.buffer`
    // across Electron's structured-clone + contextBridge can deliver the wrong
    // slice (or empty data) to the renderer — the blank-pane bug. `new
    // Uint8Array(bytes)` copies element-wise into its own backing store.
    const out = new Uint8Array(bytes);
    dataStats.total += out.byteLength;
    if (!dataStats.firstLogged) {
      dataStats.firstLogged = true;
      log.info('attachSession first data', {
        session: sessionName,
        bytes: out.byteLength,
        sinceAttachMs: Date.now() - start,
      });
    }
    sendToRenderer('term:data', { session: sessionName, chunk: out });
  });
  socket.on('close', async () => {
    attachments.delete(sessionName);
    log.info('attachSession socket close', { session: sessionName, totalBytes: dataStats.total });
    try {
      const has = await client.call({ method: 'HasSession', session: sessionName });
      const payload = has.exitCode === undefined ? { session: sessionName } : { session: sessionName, exitCode: has.exitCode };
      sendToRenderer('term:closed', payload);
    } catch (error) {
      log.error('attachSession HasSession after close failed', {
        session: sessionName,
        error: error instanceof Error ? error.message : String(error),
      });
      sendToRenderer('term:closed', { session: sessionName });
    }
  });
  socket.on('error', (error) => {
    log.error('attachSession socket error', { session: sessionName, error: error.message });
    sendToRenderer('term:error', { session: sessionName, message: error.message });
  });

  sendToRenderer('term:ready', { session: sessionName });
  log.info('attachSession ready', { session: sessionName, elapsedMs: Date.now() - start });
  // Probe after a delay so the renderer has reported its terminal rect and paint
  // has had time to (not) land — the decision signal is "did the terminal region
  // rasterize, and if so did it present?".
  const probeTimer = setTimeout(() => void capturePixelProbe(`attach ${sessionName}`, sessionName), 1500);
  probeTimer.unref?.();
  return attached;
}

function detachSession(sessionName: string): void {
  terminalRects.delete(sessionName);
  const socket = attachments.get(sessionName);
  if (socket) {
    socket.destroy();
    attachments.delete(sessionName);
  }
}

async function openRichStream(
  sessionName: string,
  since = 0,
): Promise<{ attachPipe: string; attachToken: string }> {
  const client = await getControlClient();
  const existing = eventStreams.get(sessionName);
  if (existing) {
    existing.destroy();
    eventStreams.delete(sessionName);
  }

  const stream = await client.openRichStream(sessionName, since);
  const socket = await connectEventStream(
    stream.attachPipe,
    stream.attachToken,
    (frame: EventFrame) => sendToRenderer('rich:frame', { session: sessionName, frame }),
    () => {
      const current = eventStreams.get(sessionName);
      if (current === socket) {
        eventStreams.delete(sessionName);
      }
      sendToRenderer('rich:closed', { session: sessionName });
    },
  );
  socket.once('error', (error) => {
    sendToRenderer('rich:error', { session: sessionName, message: error.message });
  });
  eventStreams.set(sessionName, socket);
  sendToRenderer('rich:ready', { session: sessionName });
  return stream;
}

function closeRichStream(sessionName: string): void {
  const socket = eventStreams.get(sessionName);
  if (socket) {
    socket.destroy();
    eventStreams.delete(sessionName);
  }
}

function detachAll(): void {
  for (const socket of attachments.values()) socket.destroy();
  attachments.clear();
  for (const socket of eventStreams.values()) socket.destroy();
  eventStreams.clear();
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
    // Auto-open DevTools when verbose logging is enabled, so users on machines
    // where the menu/accelerator are unavailable still get a renderer console.
    try {
      if (getSettings().verboseLogging && mainWindow && !mainWindow.isDestroyed()) {
        mainWindow.webContents.openDevTools({ mode: 'detach' });
      }
    } catch {
      // Best-effort; never block load on DevTools.
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
  const callHost = async (): Promise<Response> => {
    const client = await getControlClient();
    if (request.method === 'Hello' && authenticatedHello) {
      return authenticatedHello;
    }
    return client.call(request);
  };

  const started = Date.now();
  let response: Response;
  try {
    response = await callHost();
  } catch {
    // If the pipe dropped (daemon restarted), reconnect and retry once.
    if (control?.isClosed()) {
      control = null;
      authenticatedHello = null;
    }
    response = await callHost();
  }
  const elapsedMs = Date.now() - started;
  // Always time CreateWorkspace (the "stuck on Creating…" path); otherwise log
  // only slow control calls so a wedged/slow host RPC is visible in desktop.log
  // without flooding it on every fast poll.
  if (request.method === 'CreateWorkspace') {
    log.info('cs:call CreateWorkspace done', { elapsedMs, ok: response.ok, error: response.error });
  } else if (elapsedMs >= 3000) {
    log.info('cs:call slow', { method: request.method, elapsedMs, ok: response.ok });
  }
  if ((request.method === 'CreateSession' || request.method === 'CreateWorkspace') && !response.ok) {
    log.error('cs:call response error', { method: request.method, error: response.error });
  }
  return response;
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
  'rich:open-stream',
  async (_event, args: { session: string; since?: number }): Promise<{ attachPipe: string; attachToken: string }> => {
    return openRichStream(args.session, args.since ?? 0);
  },
);

ipcMain.handle('rich:close-stream', async (_event, session: string): Promise<void> => {
  closeRichStream(session);
});

ipcMain.handle('rich:send-message', async (_event, args: { session: string; message: string }): Promise<void> => {
  const client = await getControlClient();
  await client.sendMessage(args.session, args.message);
});

ipcMain.handle('rich:abort-turn', async (_event, session: string): Promise<void> => {
  const client = await getControlClient();
  await client.abortTurn(session);
});

ipcMain.handle(
  'rich:respond-permission',
  async (
    _event,
    args: { session: string; requestId: string; decision: 'approve' | 'reject' },
  ): Promise<void> => {
    const client = await getControlClient();
    await client.respondPermission(args.session, args.requestId, args.decision);
  },
);

ipcMain.handle(
  'rich:respond-user-input',
  async (
    _event,
    args: { session: string; requestId: string; answer: string; wasFreeform: boolean },
  ): Promise<void> => {
    const client = await getControlClient();
    await client.respondUserInput(args.session, args.requestId, args.answer, args.wasFreeform);
  },
);

ipcMain.handle('rich:get-transcript', async (_event, args: { session: string; since?: number }): Promise<EventFrame[]> => {
  const client = await getControlClient();
  return client.getTranscript(args.session, args.since ?? 0);
});

ipcMain.handle('rich:list-models', async (_event, session: string): Promise<ModelInfo[]> => {
  const client = await getControlClient();
  return client.listModels(session);
});

ipcMain.handle('rich:set-model', async (_event, args: { session: string; modelId: string }): Promise<void> => {
  const client = await getControlClient();
  await client.setModel(args.session, args.modelId);
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
    const programName = programNameOnly(program);
    const start = Date.now();
    log.info('cs:ensure-shell start', { session, program: programName });
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
      const createStart = Date.now();
      const created = await client.call({
        method: 'CreateSession',
        session,
        program,
        workDir: args.worktreePath,
        cols: args.cols ?? DEFAULT_COLS,
        rows: args.rows ?? DEFAULT_ROWS,
      });
      log[created.ok ? 'info' : 'error']('cs:ensure-shell CreateSession result', {
        session,
        program: programName,
        ok: created.ok,
        error: created.error,
        elapsedMs: Date.now() - createStart,
      });
      if (!created.ok) throw new Error(created.error || 'failed to start shell');
        shellSessionPrograms.set(session, program);
        log.info('cs:ensure-shell ready', { session, program: programName, created: true, elapsedMs: Date.now() - start });
      } else if (tracked === undefined) {
        // Reattached to a pre-existing session; record its assumed program so a
        // later explicit shell switch still triggers a respawn.
        shellSessionPrograms.set(session, program);
        log.info('cs:ensure-shell ready', { session, program: programName, created: false, elapsedMs: Date.now() - start });
      } else {
        log.info('cs:ensure-shell ready', { session, program: programName, created: false, elapsedMs: Date.now() - start });
      }
      return session;
  },
);

// Kill a workspace's shell session (on archive). Detaches first.
ipcMain.handle('cs:close-shell', async (_event, workspaceId: string): Promise<void> => {
  const session = `sh_${workspaceId}`;
  detachSession(session);
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


ipcMain.handle('cs:get-log-paths', async (): Promise<LogPaths> => diagnosticLogPaths());

ipcMain.handle('cs:open-log-folder', async (): Promise<void> => {
  const result = await shell.openPath(path.join(os.homedir(), '.hangar'));
  if (result) throw new Error(result);
});

ipcMain.handle('cs:open-log-file', async (_event, args: { which: LogWhich }): Promise<void> => {
  assertLogWhich(args?.which);
  const result = await shell.openPath(diagnosticLogPath(args.which));
  if (result) throw new Error(result);
});

ipcMain.handle('cs:read-log', async (_event, args: { which: LogWhich; maxBytes?: number }): Promise<ReadLogResult> => {
  assertLogWhich(args?.which);
  const file = diagnosticLogPath(args.which);
  const requested = Number(args?.maxBytes ?? DEFAULT_LOG_BYTES);
  const maxBytes = Number.isFinite(requested) && requested > 0 ? Math.floor(requested) : DEFAULT_LOG_BYTES;
  try {
    const st = await statFile(file);
    if (st.size <= maxBytes) {
      const handle = await openFile(file, 'r');
      try {
        return { path: file, content: (await handle.readFile()).toString('utf8'), truncated: false };
      } finally {
        await handle.close();
      }
    }
    const handle = await openFile(file, 'r');
    try {
      const buffer = Buffer.alloc(maxBytes);
      await handle.read(buffer, 0, maxBytes, st.size - maxBytes);
      return { path: file, content: buffer.toString('utf8'), truncated: true };
    } finally {
      await handle.close();
    }
  } catch (error) {
    if (error && typeof error === 'object' && 'code' in error && error.code === 'ENOENT') {
      return { path: file, content: '', truncated: false };
    }
    throw error;
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
    softwareCompositing,
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

// One-way diagnostics bridge: the renderer (which cannot open DevTools on
// locked-down machines, and whose console is otherwise invisible) forwards
// lifecycle events and uncaught exceptions here so they land in desktop.log,
// openable via Settings → Diagnostics.
ipcMain.on('cs:diag-log', (_event, args: { level?: 'info' | 'error'; event?: string; data?: unknown }) => {
  const event = typeof args?.event === 'string' ? args.event : '(no event)';
  const data = args?.data;
  if (args?.level === 'error') {
    if (data === undefined) log.error('[renderer]', event);
    else log.error('[renderer]', event, data);
  } else {
    if (data === undefined) log.info('[renderer]', event);
    else log.info('[renderer]', event, data);
  }
});

// cs:set-terminal-rect lets the renderer report a session's terminal pane rect
// (CSS px, viewport-relative) so capturePixelProbe can capture just that region —
// isolating the terminal's pixels from the always-painting React UI. Best-effort.
ipcMain.on(
  'cs:set-terminal-rect',
  (
    _event,
    args: { session?: string; x?: number; y?: number; width?: number; height?: number },
  ) => {
    const s = args?.session;
    if (!s) return;
    const x = Math.max(0, Math.round(args.x ?? 0));
    const y = Math.max(0, Math.round(args.y ?? 0));
    const width = Math.round(args.width ?? 0);
    const height = Math.round(args.height ?? 0);
    if (width >= 1 && height >= 1) {
      terminalRects.set(s, { x, y, width, height });
    }
  },
);

// Open DevTools on demand. The app hides its menu and the Ctrl+Shift+I
// accelerator can be suppressed by policy, so this gives users a reliable way
// to inspect the renderer console (surfaced in Settings → Diagnostics).
ipcMain.handle('cs:open-devtools', async (): Promise<void> => {
  if (mainWindow && !mainWindow.isDestroyed()) {
    mainWindow.webContents.openDevTools({ mode: 'detach' });
  }
});

// cs:get-render-info exposes the resolved compositing/nudge state to the renderer
// so TermView can gate its renderer-only nudge + diagnostics. softwareCompositing
// is only known post-ready, so the renderer reads it lazily after the window opens.
ipcMain.handle('cs:get-render-info', async () => {
  let terminalDiagnostics = false;
  let terminalRenderer: 'auto' | 'dom' | 'canvas' = 'auto';
  try {
    const s = getSettings();
    terminalDiagnostics = s.terminalDiagnostics ?? false;
    terminalRenderer = s.terminalRenderer ?? 'auto';
  } catch {
    // Fall back to defaults if settings are unreadable.
  }
  return {
    softwareCompositing,
    windowOcclusionDisabled,
    hardwareAccelerationDisabled,
    remoteSession: isRemoteSession(),
    terminalDiagnostics,
    terminalRenderer,
  };
});

app.whenReady().then(() => {
  // A losing single-instance process already called app.quit(); never build a
  // window/tray/host client for it.
  if (!isPrimaryInstance) return;
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
  // Record the GPU/acceleration state so a blank-terminal report can be diagnosed
  // from desktop.log: confirms whether hardware acceleration is off and how
  // Chromium resolved each GPU feature (e.g. software-only compositing). The
  // resolved `softwareCompositing` flag also enables the forced-repaint workaround
  // for RDP/VDI sessions where xterm updates the DOM but no paint is flushed.
  try {
    const featureStatus = app.getGPUFeatureStatus() as unknown as Record<string, unknown>;
    softwareCompositing = isSoftwareCompositing(featureStatus, hardwareAccelerationDisabled);
    let terminalDiagnostics = false;
    try {
      terminalDiagnostics = getSettings().terminalDiagnostics ?? false;
    } catch {
      // Defaults already set.
    }
    log.info('gpu status', {
      hardwareAccelerationDisabled,
      windowOcclusionDisabled,
      directCompositionDisabled,
      softwareCompositing,
      remoteSession: isRemoteSession(),
      terminalDiagnostics,
      featureStatus,
    });
  } catch (error) {
    log.error('gpu status failed', error);
  }
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

// shutdownHost stops the per-user session-host so `cs.exe` exits when the desktop
// app really quits (not when merely minimized to the tray). It prefers the graceful
// Shutdown RPC — which closes every session, stops runs, and exits the host without
// orphaning agent child processes — and falls back to force-killing the host process
// tree by PID so the daemon never lingers. Only the primary instance owns the host.
async function shutdownHost(): Promise<void> {
  if (!isPrimaryInstance) return;
  const hostPid = authenticatedHello?.hostPid;
  let stopped = false;
  if (control && !control.isClosed()) {
    stopped = await requestHostShutdown(control);
  }
  if (!stopped && typeof hostPid === 'number') {
    killHostProcess(hostPid);
  }
}

// finalizeQuit runs the one-time quit teardown asynchronously (the host Shutdown RPC
// is awaited), then re-issues the quit. `before-quit` defers to this and is re-entrant
// safe via quitTeardownDone.
async function finalizeQuit(): Promise<void> {
  try {
    globalShortcut.unregisterAll();
    destroyTray();
    await shutdownHost();
    detachAll();
    if (control) {
      control.close();
      control = null;
    }
  } catch (error) {
    log.error('finalizeQuit error', error);
  } finally {
    quitTeardownDone = true;
    app.quit();
  }
}

app.on('before-quit', (e) => {
  isQuitting = true;
  // Second pass (re-issued by finalizeQuit): let the real quit proceed.
  if (quitTeardownDone) return;
  // First pass: defer the quit so the host can be shut down gracefully (async),
  // then quit for real once teardown completes.
  e.preventDefault();
  void finalizeQuit();
});

app.on('window-all-closed', () => {
  // Route through app.quit() → before-quit, which owns the full teardown (host
  // shutdown + cleanup). Workspaces/branches persist on disk; the live host is
  // stopped so `cs.exe` exits with the app.
  app.quit();
});
