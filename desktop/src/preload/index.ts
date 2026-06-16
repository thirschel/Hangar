import { contextBridge, ipcRenderer } from 'electron';
import type { DirEntry, FileContents, FileDiffInfo, Request, Response, WorkspaceInfo } from '../main/host-client';
import type { Settings } from '../main/settings';

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
  title: string;
  program?: string;
  baseBranch?: string;
  autoYes?: boolean;
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

const rpc = (request: Omit<Request, 'id'>): Promise<Response> => ipcRenderer.invoke('cs:call', request);

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
    });
    if (!r.ok || !r.workspace) throw new Error(r.error || 'CreateWorkspace failed');
    return r.workspace;
  },
  archiveWorkspace: async (id: string): Promise<void> => {
    const r = await rpc({ method: 'ArchiveWorkspace', workspaceId: id });
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
  attachSession: (sessionName: string, size?: { cols?: number; rows?: number }): Promise<Response> =>
    ipcRenderer.invoke('cs:attach-session', { sessionName, ...size }),
  detachSession: (sessionName: string): Promise<void> => ipcRenderer.invoke('cs:detach-session', sessionName),
  ensureShell: (workspaceId: string, worktreePath: string, size?: { cols?: number; rows?: number }): Promise<string> =>
    ipcRenderer.invoke('cs:ensure-shell', { workspaceId, worktreePath, ...size }),
  closeShell: (workspaceId: string): Promise<void> => ipcRenderer.invoke('cs:close-shell', workspaceId),
  pickFolder: (): Promise<string | null> => ipcRenderer.invoke('cs:pick-folder'),
  getDefaultProgram: (): Promise<string> => ipcRenderer.invoke('cs:get-default-program'),
  openExternal: (url: string): Promise<void> => ipcRenderer.invoke('cs:open-external', url),
  getSettings: (): Promise<Settings> => ipcRenderer.invoke('cs:get-settings'),
  setSettings: (patch: Partial<Settings>): Promise<Settings> => ipcRenderer.invoke('cs:set-settings', patch),
  notify: (n: { title: string; body: string; workspaceId?: string }): Promise<void> =>
    ipcRenderer.invoke('cs:notify', n),

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
  onFocusWorkspace: (callback: (workspaceId: string) => void): Unsubscribe => on('cs:focus-workspace', callback),
  sendInput: (session: string, data: string): void => ipcRenderer.send('term:input', { session, data }),
  resize: (session: string, cols: number, rows: number): void =>
    ipcRenderer.send('term:resize', { session, cols, rows }),
};

contextBridge.exposeInMainWorld('cs', api);

export type CsApi = typeof api;
