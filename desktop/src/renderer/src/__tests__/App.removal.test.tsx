// @vitest-environment jsdom
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { WorkspaceInfo } from '../../../main/host-client';
import { PROTO_VERSION } from '../../../shared/proto-version';

// Keep the standard-mode center/right panes light; we exercise the real Sidebar
// (the deleting row) and the real RemoveWorkspaceModal so the whole removal flow
// runs end to end.
vi.mock('../components/CenterPane', () => ({
  CenterPane: () => <section className="center-pane">Center pane</section>,
}));
vi.mock('../components/RightPanel', () => ({
  RightPanel: () => <aside className="right-panel">Right panel</aside>,
}));

import { App } from '../App';

function workspace(overrides: Partial<WorkspaceInfo>): WorkspaceInfo {
  return {
    id: 'a',
    title: 'Alpha',
    program: 'copilot',
    repoPath: 'C:\\src\\Hangar',
    worktreePath: 'C:\\src\\Hangar\\.hangar',
    branch: 'feature',
    sessionName: 'ws_a',
    alive: true,
    autoYes: false,
    added: 0,
    removed: 0,
    createdUnix: 1,
    lastOutputUnix: 0,
    runCommand: '',
    running: false,
    previewUrl: '',
    busy: false,
    waiting: false,
    regenerating: false,
    shell: 'cmd',
    hasWorktree: true,
    ...overrides,
  };
}

function createStorageMock(): Storage {
  const store = new Map<string, string>();
  return {
    get length(): number {
      return store.size;
    },
    clear(): void {
      store.clear();
    },
    getItem(key: string): string | null {
      const value = store.get(key);
      return value === undefined ? null : value;
    },
    key(index: number): string | null {
      return Array.from(store.keys())[index] ?? null;
    },
    removeItem(key: string): void {
      store.delete(key);
    },
    setItem(key: string, value: string): void {
      store.set(key, String(value));
    },
  };
}

beforeEach(() => {
  vi.stubGlobal('localStorage', createStorageMock());
});

afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

describe('App non-blocking removal', () => {
  it('marks the row deleting and keeps the app responsive while the slow archive runs', async () => {
    // The host lists one workspace until the archive completes.
    let list: WorkspaceInfo[] = [workspace({})];
    window.cs.listWorkspaces = vi.fn(async () => list);
    // Resolve the connect handshake so the first refresh populates the row.
    window.cs.call = vi.fn(async () => ({ hostVersion: PROTO_VERSION }) as never);

    // A controllable, slow archive: it stays pending until we resolve it.
    let resolveArchive!: () => void;
    const archived = new Promise<void>((r) => {
      resolveArchive = () => r();
    });
    const archiveSpy = vi.fn(() => archived);
    window.cs.archiveWorkspace = archiveSpy as never;
    const closeSpy = vi.fn(async () => {});
    window.cs.closeShell = closeSpy as never;

    await act(async () => {
      render(<App />);
    });
    // Wait for connect + first refresh to render the workspace row.
    await screen.findByText('Alpha');

    // Open the remove modal from the row's archive action, then confirm.
    fireEvent.click(screen.getByTitle('Archive workspace (D)'));
    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: /^Remove workspace$/i }));
    });

    // Non-blocking: the archive RPC is kicked off but still pending, yet the row
    // is already marked "Deleting…" (we did not await the slow call).
    expect(archiveSpy).toHaveBeenCalledWith('a', { deleteWorktree: false });
    expect(await screen.findByText('Deleting…')).toBeInTheDocument();

    // Completing the slow archive prunes the row and closes its shell.
    list = [];
    await act(async () => {
      resolveArchive();
      await archived;
    });
    await waitFor(() => expect(screen.queryByText('Alpha')).toBeNull());
    expect(closeSpy).toHaveBeenCalledWith('a');
  });
});
