import { contextBridge, ipcRenderer } from 'electron';
import type {
  CopilotSessionInfo,
  DirEntry,
  EventFrame,
  FileContents,
  FileDiffInfo,
  ModelInfo,
  Request,
  Response,
  WorkspaceInfo,
} from '../main/host-client';
import type { McpCatalog, McpServerDef } from '../main/mcpCatalog';
import type { Settings, ShellProfile } from '../main/settings';

export type { McpCatalog, McpServerDef } from '../main/mcpCatalog';

export type AppInfo = {
  version: string;
  appName: string;
  electronVersion: string;
  nodeVersion: string;
  platform: string;
  arch: string;
  githubUrl: string;
  author: string;
  softwareCompositing: boolean;
};

export type UpdateStatus = {
  status: 'idle' | 'checking' | 'available' | 'not-available' | 'downloading' | 'downloaded' | 'error';
  version?: string;
  progress?: number;
  error?: string;
};

export type ReadyInfo = {
  session: string;
};

export type TermData = {
  session: string;
  chunk: Uint8Array;
};

export type TermClosed = {
  session: string;
  exitCode?: number;
};

export type TermError = {
  session: string;
  message: string;
};

export type RichFrame = {
  session: string;
  frame: EventFrame;
};

export type RichReady = {
  session: string;
};

export type RichClosed = {
  session: string;
};

export type RichError = {
  session: string;
  message: string;
};

// Resolved render/compositing state (RDP blank-terminal mitigations). The renderer
// reads this lazily after the window opens to select the terminal renderer and gate
// its diagnostics probe. See docs/rdp-blank-terminal-postmortem.md.
export type RenderInfo = {
  softwareCompositing: boolean;
  windowOcclusionDisabled: boolean;
  hardwareAccelerationDisabled: boolean;
  remoteSession: boolean;
  terminalDiagnostics: boolean;
  terminalRenderer: 'auto' | 'dom' | 'canvas';
};

export type HostReadyInfo = {
  hostVersion?: number;
  ok: boolean;
};

export type CreateWorkspaceArgs = {
  repoPath: string;
  title?: string;
  program?: string;
  baseBranch?: string;
  autoYes?: boolean;
  shell?: string;
  // When true, open the session in-place against repoPath without a git worktree.
  noWorktree?: boolean;
  // When true (Copilot only), use the experimental rich Copilot SDK agent view.
  rich?: boolean;
};

export type RunOutput = {
  data: string;
  nextOffset: number;
  running: boolean;
  exitCode: number;
};

export type LogWhich = 'host' | 'desktop' | 'hangar';
export type LogPaths = { hostLog: string; desktopLog: string; hangarLog: string };
export type LogContent = { path: string; content: string; truncated: boolean };

type Unsubscribe = () => void;

const on = <T>(channel: string, callback: (payload: T) => void): Unsubscribe => {
  const listener = (_event: Electron.IpcRendererEvent, payload: T): void => callback(payload);
  ipcRenderer.on(channel, listener);
  return () => ipcRenderer.removeListener(channel, listener);
};

const rpc = (request: Omit<Request, 'id'>): Promise<Response> =>
  ipcRenderer.invoke('cs:call', request);

const api = {
  call: rpc,

  // Workspace operations (v2 core-daemon surface).
  listWorkspaces: async (): Promise<WorkspaceInfo[]> => {
    const r = await rpc({ method: 'ListWorkspaces' });
    if (!r.ok) throw new Error(r.error || 'ListWorkspaces failed');
    return r.workspaces ?? [];
  },
  createWorkspace: async (args: CreateWorkspaceArgs): Promise<WorkspaceInfo> => {
    const r = await rpc({
      method: 'CreateWorkspace',
      repoPath: args.repoPath,
      title: args.title,
      program: args.program,
      baseBranch: args.baseBranch,
      autoYes: args.autoYes,
      shell: args.shell,
      noWorktree: args.noWorktree,
      rich: args.rich,
    });
    if (!r.ok || !r.workspace) throw new Error(r.error || 'CreateWorkspace failed');
    return r.workspace;
  },
  generateWorkspaceTitle: async (workspaceId: string, message: string): Promise<void> => {
    const r = await rpc({ method: 'GenerateWorkspaceTitle', workspaceId, message });
    if (!r.ok) throw new Error(r.error || 'GenerateWorkspaceTitle failed');
  },
  archiveWorkspace: async (id: string, options?: { deleteWorktree?: boolean }): Promise<void> => {
    const r = await rpc({ 
      method: 'ArchiveWorkspace', 
      workspaceId: id,
      deleteWorktree: options?.deleteWorktree ?? false,
    });
    if (!r.ok) throw new Error(r.error || 'ArchiveWorkspace failed');
  },
  workspaceFiles: async (id: string): Promise<FileDiffInfo[]> => {
    const r = await rpc({ method: 'WorkspaceDiff', workspaceId: id });
    if (!r.ok) throw new Error(r.error || 'WorkspaceDiff failed');
    return r.files ?? [];
  },
  workspaceFileDiff: async (id: string, file: string): Promise<string> => {
    const r = await rpc({ method: 'WorkspaceDiff', workspaceId: id, file });
    if (!r.ok) throw new Error(r.error || 'WorkspaceDiff failed');
    return r.diff ?? '';
  },
  setWorkspaceAutoYes: async (id: string, enabled: boolean): Promise<void> => {
    const r = await rpc({ method: 'SetWorkspaceAutoYes', workspaceId: id, enabled });
    if (!r.ok) throw new Error(r.error || 'SetWorkspaceAutoYes failed');
  },
  regenerateAgent: async (
    id: string,
    handoff: boolean,
    size?: { cols?: number; rows?: number },
  ): Promise<void> => {
    const r = await rpc({
      method: 'RegenerateAgent',
      workspaceId: id,
      handoff,
      cols: size?.cols,
      rows: size?.rows,
    });
    if (!r.ok) throw new Error(r.error || 'RegenerateAgent failed');
  },
  forceRegenerate: async (id: string): Promise<void> => {
    const r = await rpc({ method: 'ForceRegenerate', workspaceId: id });
    if (!r.ok) throw new Error(r.error || 'ForceRegenerate failed');
  },
  commitWorkspace: async (id: string, message: string): Promise<string> => {
    const r = await rpc({ method: 'WorkspaceCommit', workspaceId: id, message });
    if (!r.ok) throw new Error(r.error || 'WorkspaceCommit failed');
    return r.content ?? '';
  },
  pushWorkspace: async (id: string): Promise<string> => {
    const r = await rpc({ method: 'WorkspacePush', workspaceId: id });
    if (!r.ok) throw new Error(r.error || 'WorkspacePush failed');
    return r.content ?? '';
  },
  updateWorkspace: async (
    id: string,
    patch: { title?: string; program?: string; shell?: string },
  ): Promise<WorkspaceInfo> => {
    const r = await rpc({
      method: 'UpdateWorkspace',
      workspaceId: id,
      title: patch.title,
      program: patch.program,
      shell: patch.shell,
    });
    if (!r.ok || !r.workspace) throw new Error(r.error || 'UpdateWorkspace failed');
    return r.workspace;
  },
  listCopilotSessions: async (): Promise<{ sessions: CopilotSessionInfo[]; skipped: number }> => {
    const r = await rpc({ method: 'ListCopilotSessions' });
    if (!r.ok) throw new Error(r.error || 'ListCopilotSessions failed');
    return { sessions: r.copilotSessions ?? [], skipped: r.skipped ?? 0 };
  },
  resumeCopilotSession: async (
    sessionId: string,
    opts?: { repoPath?: string; title?: string; confirmed?: boolean },
  ): Promise<{ workspace?: WorkspaceInfo; needsConfirm?: boolean; absPath?: string }> => {
    const r = await rpc({
      method: 'ResumeCopilotSession',
      sessionId,
      repoPath: opts?.repoPath,
      title: opts?.title,
      confirmed: opts?.confirmed,
    });
    // The host returns needsConfirm (with the resolved absolute path) when the
    // resume targets a repo other than its own working directory and has not yet
    // been confirmed. Surface that to the renderer instead of throwing.
    if (r.needsConfirm) {
      return { needsConfirm: true, absPath: r.absPath };
    }
    if (!r.ok || !r.workspace) throw new Error(r.error || 'ResumeCopilotSession failed');
    return { workspace: r.workspace };
  },
  startRun: async (id: string, command: string): Promise<void> => {
    const r = await rpc({ method: 'StartRun', workspaceId: id, command });
    if (!r.ok) throw new Error(r.error || 'StartRun failed');
  },
  stopRun: async (id: string): Promise<void> => {
    const r = await rpc({ method: 'StopRun', workspaceId: id });
    if (!r.ok) throw new Error(r.error || 'StopRun failed');
  },
  workspaceRunOutput: async (id: string, sinceOffset: number): Promise<RunOutput> => {
    const r = await rpc({ method: 'WorkspaceRunOutput', workspaceId: id, sinceOffset });
    if (!r.ok) throw new Error(r.error || 'WorkspaceRunOutput failed');
    const data = r.data ? Buffer.from(r.data, 'base64').toString('utf8') : '';
    return {
      data,
      nextOffset: r.nextOffset ?? sinceOffset,
      running: r.runRunning ?? false,
      exitCode: r.exitCode ?? 0,
    };
  },

  // Terminal streams (session-scoped: agent + shell can be live at once).
  attachSession: (
    sessionName: string,
    size?: { cols?: number; rows?: number },
  ): Promise<Response> => ipcRenderer.invoke('cs:attach-session', { sessionName, ...size }),
  detachSession: (sessionName: string): Promise<void> =>
    ipcRenderer.invoke('cs:detach-session', sessionName),
  getHistory: (
    session: string,
    includeScreen = false,
    size?: { cols?: number; rows?: number },
  ): Promise<{ ansi: string; altScreen: boolean; scrollbackLines: number }> =>
    ipcRenderer.invoke('cs:get-history', { session, includeScreen, ...size }),
  ensureShell: (
    workspaceId: string,
    worktreePath: string,
    opts?: { cols?: number; rows?: number; program?: string },
  ): Promise<string> =>
    ipcRenderer.invoke('cs:ensure-shell', { workspaceId, worktreePath, ...opts }),
  closeShell: (workspaceId: string): Promise<void> =>
    ipcRenderer.invoke('cs:close-shell', workspaceId),
  openRichStream: (
    session: string,
    since = 0,
  ): Promise<{ attachPipe: string; attachToken: string }> =>
    ipcRenderer.invoke('rich:open-stream', { session, since }),
  closeRichStream: (session: string): Promise<void> =>
    ipcRenderer.invoke('rich:close-stream', session),
  sendMessage: (session: string, message: string, attachments?: string[], agentMode?: string): Promise<void> =>
    ipcRenderer.invoke('rich:send-message', { session, message, attachments, agentMode }),
  abortTurn: (session: string): Promise<void> =>
    ipcRenderer.invoke('rich:abort-turn', session),
  respondPermission: (
    session: string,
    requestId: string,
    decision: 'approve' | 'reject',
  ): Promise<void> =>
    ipcRenderer.invoke('rich:respond-permission', { session, requestId, decision }),
  respondUserInput: (
    session: string,
    requestId: string,
    answer: string,
    wasFreeform: boolean,
  ): Promise<void> =>
    ipcRenderer.invoke('rich:respond-user-input', { session, requestId, answer, wasFreeform }),
  respondExitPlanMode: (
    session: string,
    requestId: string,
    approved: boolean,
    selectedAction: string,
    feedback: string,
  ): Promise<void> =>
    ipcRenderer.invoke('rich:respond-exit-plan-mode', { session, requestId, approved, selectedAction, feedback }),
  getTranscript: (session: string, since = 0): Promise<EventFrame[]> =>
    ipcRenderer.invoke('rich:get-transcript', { session, since }),
  // Live model selector (session-scoped, same session id as the other rich calls).
  listModels: (sessionName: string): Promise<ModelInfo[]> =>
    ipcRenderer.invoke('rich:list-models', sessionName),
  setModel: (
    sessionName: string,
    modelId: string,
    effort?: string,
    contextTier?: string,
  ): Promise<void> =>
    ipcRenderer.invoke('rich:set-model', { session: sessionName, modelId, effort, contextTier }),
  detectShells: (): Promise<ShellProfile[]> => ipcRenderer.invoke('cs:detect-shells'),
  pickFolder: (): Promise<string | null> => ipcRenderer.invoke('cs:pick-folder'),
  // Native multi-select open-file dialog for message attachments; returns the
  // chosen absolute paths (or [] when canceled). Mirrors pickFolder's wiring.
  pickFiles: (): Promise<string[]> => ipcRenderer.invoke('cs:pick-files'),
  getDefaultProgram: (): Promise<string> => ipcRenderer.invoke('cs:get-default-program'),
  openExternal: (url: string): Promise<void> => ipcRenderer.invoke('cs:open-external', url),
  // Terminal clipboard goes through the main-process clipboard module (Electron's
  // navigator.clipboard is unreliable from xterm's keydown path and gates readText
  // behind the clipboard-read permission). See TermView copySelection()/paste().
  clipboardWrite: (text: string): Promise<void> => ipcRenderer.invoke('cs:clipboard-write', text),
  clipboardRead: (): Promise<string> => ipcRenderer.invoke('cs:clipboard-read'),
  getSettings: (): Promise<Settings> => ipcRenderer.invoke('cs:get-settings'),
  mcpRead: (): Promise<McpCatalog> => ipcRenderer.invoke('cs:mcp-read'),
  mcpUpsertServer: (name: string, def: McpServerDef): Promise<McpCatalog> =>
    ipcRenderer.invoke('cs:mcp-upsert-server', name, def),
  mcpRemoveServer: (name: string): Promise<McpCatalog> =>
    ipcRenderer.invoke('cs:mcp-remove-server', name),
  mcpSetEnabled: (repoKey: string, name: string, enabled: boolean): Promise<McpCatalog> =>
    ipcRenderer.invoke('cs:mcp-set-enabled', repoKey, name, enabled),
  // RDP blank-terminal mitigations: resolved compositing state used to select the
  // terminal renderer (canvas under software compositing) and gate diagnostics.
  getRenderInfo: (): Promise<RenderInfo> => ipcRenderer.invoke('cs:get-render-info'),
  // Report a session's terminal pane rect (CSS px, viewport-relative) so the main
  // process can isolate the terminal region in its capturePage diagnostics probe.
  setTerminalRect: (
    session: string,
    rect: { x: number; y: number; width: number; height: number },
  ): void => ipcRenderer.send('cs:set-terminal-rect', { session, ...rect }),
  getLogPaths: (): Promise<LogPaths> => ipcRenderer.invoke('cs:get-log-paths'),
  openLogFolder: (): Promise<void> => ipcRenderer.invoke('cs:open-log-folder'),
  openLogFile: (which: LogWhich): Promise<void> => ipcRenderer.invoke('cs:open-log-file', { which }),
  readLog: (which: LogWhich, maxBytes?: number): Promise<LogContent> =>
    ipcRenderer.invoke('cs:read-log', { which, maxBytes }),
  setSettings: (patch: Partial<Settings>): Promise<Settings> =>
    ipcRenderer.invoke('cs:set-settings', patch),
  getAppInfo: (): Promise<AppInfo> => ipcRenderer.invoke('cs:get-app-info'),
  checkForUpdate: (): Promise<UpdateStatus> => ipcRenderer.invoke('cs:check-for-update'),
  downloadUpdate: (): Promise<void> => ipcRenderer.invoke('cs:download-update'),
  installUpdate: (): Promise<void> => ipcRenderer.invoke('cs:install-update'),
  notify: (n: { title: string; body: string; workspaceId?: string }): Promise<void> =>
    ipcRenderer.invoke('cs:notify', n),
  setBadge: (count: number): Promise<void> => ipcRenderer.invoke('cs:set-badge', count),

  // Files tab (read-only worktree browser).
  listDir: (worktreePath: string, relDir: string): Promise<DirEntry[]> =>
    ipcRenderer.invoke('cs:fs-list', { worktreePath, relDir }),
  readFile: (worktreePath: string, relFile: string): Promise<FileContents> =>
    ipcRenderer.invoke('cs:fs-read', { worktreePath, relFile }),

  onData: (callback: (data: TermData) => void): Unsubscribe => on('term:data', callback),
  onReady: (callback: (info: ReadyInfo) => void): Unsubscribe => on('term:ready', callback),
  onHostReady: (callback: (info: HostReadyInfo) => void): Unsubscribe => on('cs:ready', callback),
  onClosed: (callback: (info: TermClosed) => void): Unsubscribe => on('term:closed', callback),
  onError: (callback: (info: TermError) => void): Unsubscribe => on('term:error', callback),
  onRichFrame: (callback: (data: RichFrame) => void): Unsubscribe => on('rich:frame', callback),
  onRichReady: (callback: (info: RichReady) => void): Unsubscribe => on('rich:ready', callback),
  onRichClosed: (callback: (info: RichClosed) => void): Unsubscribe => on('rich:closed', callback),
  onRichError: (callback: (info: RichError) => void): Unsubscribe => on('rich:error', callback),
  onUpdateStatus: (callback: (status: UpdateStatus) => void): Unsubscribe =>
    on('cs:update-status', callback),
  onFirstRun: (callback: () => void): Unsubscribe => on('cs:first-run', callback),
  onFocusWorkspace: (callback: (workspaceId: string) => void): Unsubscribe =>
    on('cs:focus-workspace', callback),
  onPlayNotificationSound: (callback: () => void): Unsubscribe =>
    on('cs:play-notification-sound', callback),
  onMcpChanged: (callback: (catalog: McpCatalog) => void): Unsubscribe =>
    on('mcp:changed', callback),
  completeSetup: (opts: { autoUpdate: boolean }): Promise<void> =>
    ipcRenderer.invoke('cs:complete-setup', opts),
  sendInput: (session: string, data: string): void =>
    ipcRenderer.send('term:input', { session, data }),
  resize: (session: string, cols: number, rows: number): void =>
    ipcRenderer.send('term:resize', { session, cols, rows }),
  // Renderer diagnostics: forward an event (and optional structured data) to the
  // main process so it lands in desktop.log. One-way; never throws.
  diag: (event: string, data?: unknown, level: 'info' | 'error' = 'info'): void =>
    ipcRenderer.send('cs:diag-log', { event, data, level }),
  openDevTools: (): Promise<void> => ipcRenderer.invoke('cs:open-devtools'),
};

contextBridge.exposeInMainWorld('cs', api);

export type CsApi = typeof api;
