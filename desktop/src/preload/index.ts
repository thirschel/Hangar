import { contextBridge, ipcRenderer } from 'electron';
import type { FileDiffInfo, Request, Response, WorkspaceInfo } from '../main/host-client';

export type ReadyInfo = {
  session: string;
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

  // Terminal attach for the selected workspace.
  attachWorkspace: (sessionName: string, size?: { cols?: number; rows?: number }): Promise<Response> =>
    ipcRenderer.invoke('cs:attach-workspace', { sessionName, ...size }),
  detach: (): Promise<void> => ipcRenderer.invoke('cs:detach'),
  pickFolder: (): Promise<string | null> => ipcRenderer.invoke('cs:pick-folder'),
  getDefaultProgram: (): Promise<string> => ipcRenderer.invoke('cs:get-default-program'),
  openExternal: (url: string): Promise<void> => ipcRenderer.invoke('cs:open-external', url),

  onData: (callback: (chunk: Uint8Array) => void): Unsubscribe => on('term:data', callback),
  onReady: (callback: (info: ReadyInfo) => void): Unsubscribe => on('term:ready', callback),
  onHostReady: (callback: (info: HostReadyInfo) => void): Unsubscribe => on('cs:ready', callback),
  onClosed: (callback: () => void): Unsubscribe => on('term:closed', callback),
  onError: (callback: (message: string) => void): Unsubscribe => on('term:error', callback),
  sendInput: (data: string): void => ipcRenderer.send('term:input', data),
  resize: (cols: number, rows: number): void => ipcRenderer.send('term:resize', { cols, rows }),
};

contextBridge.exposeInMainWorld('cs', api);

export type CsApi = typeof api;
