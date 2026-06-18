import { contextBridge, ipcRenderer } from 'electron';
import type {
  CopilotSessionInfo,
  DirEntry,
  FileContents,
  FileDiffInfo,
  Request,
  Response,
  WorkspaceInfo,
} from '../main/host-client';
import type { Settings } from '../main/settings';

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
};

export type TermError = {
  session: string;
  message: string;
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
};

export type RunOutput = {
  data: string;
  nextOffset: number;
  running: boolean;
  exitCode: number;
};

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
  ): Promise<{ ansi: string; altScreen: boolean; scrollbackLines: number }> =>
    ipcRenderer.invoke('cs:get-history', { session, includeScreen }),
  ensureShell: (
    workspaceId: string,
    worktreePath: string,
    size?: { cols?: number; rows?: number },
  ): Promise<string> =>
    ipcRenderer.invoke('cs:ensure-shell', { workspaceId, worktreePath, ...size }),
  closeShell: (workspaceId: string): Promise<void> =>
    ipcRenderer.invoke('cs:close-shell', workspaceId),
  pickFolder: (): Promise<string | null> => ipcRenderer.invoke('cs:pick-folder'),
  getDefaultProgram: (): Promise<string> => ipcRenderer.invoke('cs:get-default-program'),
  openExternal: (url: string): Promise<void> => ipcRenderer.invoke('cs:open-external', url),
  getSettings: (): Promise<Settings> => ipcRenderer.invoke('cs:get-settings'),
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
  onUpdateStatus: (callback: (status: UpdateStatus) => void): Unsubscribe =>
    on('cs:update-status', callback),
  onFirstRun: (callback: () => void): Unsubscribe => on('cs:first-run', callback),
  onFocusWorkspace: (callback: (workspaceId: string) => void): Unsubscribe =>
    on('cs:focus-workspace', callback),
  completeSetup: (opts: { autoUpdate: boolean }): Promise<void> =>
    ipcRenderer.invoke('cs:complete-setup', opts),
  sendInput: (session: string, data: string): void =>
    ipcRenderer.send('term:input', { session, data }),
  resize: (session: string, cols: number, rows: number): void =>
    ipcRenderer.send('term:resize', { session, cols, rows }),
};

contextBridge.exposeInMainWorld('cs', api);

export type CsApi = typeof api;
