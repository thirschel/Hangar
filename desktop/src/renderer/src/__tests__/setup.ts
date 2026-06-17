import '@testing-library/jest-dom/vitest';
import { beforeEach } from 'vitest';

// Mock the preload API that the renderer accesses via window.cs.
const noop = () => {};
const asyncNoop = async () => {};

const mockCs = {
  call: asyncNoop,
  listWorkspaces: async () => [],
  createWorkspace: async () => ({} as never),
  generateWorkspaceTitle: asyncNoop,
  archiveWorkspace: asyncNoop,
  workspaceFiles: async () => [],
  workspaceFileDiff: async () => '',
  setWorkspaceAutoYes: asyncNoop,
  regenerateAgent: asyncNoop,
  forceRegenerate: asyncNoop,
  commitWorkspace: async () => '',
  pushWorkspace: async () => '',
  updateWorkspace: async () => ({} as never),
  listCopilotSessions: async () => ({ sessions: [], skipped: 0 }),
  resumeCopilotSession: async () => ({} as never),
  startRun: asyncNoop,
  stopRun: asyncNoop,
  workspaceRunOutput: async () => ({ data: '', nextOffset: 0, running: false, exitCode: 0 }),
  attachSession: async () => ({ id: 0, ok: true }),
  detachSession: asyncNoop,
  ensureShell: async () => 'sh_mock',
  closeShell: asyncNoop,
  pickFolder: async () => null,
  getDefaultProgram: async () => 'copilot',
  openExternal: asyncNoop,
  getSettings: async () => ({
    defaultProgram: 'copilot',
    defaultShell: 'cmd',
    autoYes: false,
    branchPrefix: '',
    workspaceDir: '',
    notifications: true,
    minimizeToTray: true,
    uiRefreshMs: 2000,
  }),
  setSettings: async () => ({} as never),
  notify: asyncNoop,
  setBadge: asyncNoop,
  listDir: async () => [],
  readFile: async () => ({ kind: 'text' as const, text: '' }),
  onData: () => noop,
  onReady: () => noop,
  onHostReady: () => noop,
  onClosed: () => noop,
  onError: () => noop,
  onFocusWorkspace: () => noop,
  sendInput: noop,
  resize: noop,
};

function installMockCs(): void {
  const target = typeof window !== 'undefined' ? window : globalThis;
  Object.defineProperty(target, 'cs', { value: mockCs, writable: true, configurable: true });
}

installMockCs();
beforeEach(() => {
  installMockCs();
});
