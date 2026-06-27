// @vitest-environment jsdom
import { act, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { WorkspaceInfo } from '../../../main/host-client';
import { PROTO_VERSION } from '../../../shared/proto-version';

// Keep the standard-mode panes and the heavy tile bodies light; we exercise the
// real Sidebar (grid checkboxes) + the real GridPane, and assert each rich tile
// mounts a ChatViewHost (mocked to a marker).
vi.mock('../components/CenterPane', () => ({
  CenterPane: () => <section className="center-pane">Center pane</section>,
}));
vi.mock('../components/RightPanel', () => ({
  RightPanel: () => <aside className="right-panel">Right panel</aside>,
}));
vi.mock('../components/TermView', () => ({
  TermView: () => <div data-testid="termview" />,
}));
vi.mock('../components/ChatViewHost', () => ({
  ChatViewHost: ({ workspace }: { workspace: WorkspaceInfo }) => (
    <div data-testid="rich-tile" data-id={workspace.id} />
  ),
}));

import { App } from '../App';

function workspace(overrides: Partial<WorkspaceInfo>): WorkspaceInfo {
  return {
    id: 'a',
    title: 'Chat A',
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
    kind: 'rich',
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

describe('App agent grid', () => {
  it('tiles the selected rich chats when grid is toggled in agent mode', async () => {
    // Start in the agent surface with two rich chats listed.
    localStorage.setItem('cs.appMode', 'agent');
    window.cs.listWorkspaces = vi.fn(async () => [
      workspace({ id: 'a', title: 'Chat A', sessionName: 'ws_a' }),
      workspace({ id: 'b', title: 'Chat B', sessionName: 'ws_b' }),
    ]);
    window.cs.call = vi.fn(async () => ({ hostVersion: PROTO_VERSION }) as never);

    await act(async () => {
      render(<App />);
    });
    // Both chats show in the agent sidebar.
    await screen.findByText('Chat A');
    await screen.findByText('Chat B');

    // The Grid button is disabled until 2+ chats are selected via the checkboxes.
    const gridButton = screen.getByRole('button', { name: /toggle agent grid/i });
    expect(gridButton).toBeDisabled();

    fireEvent.click(screen.getByRole('checkbox', { name: 'Add Chat A to grid' }));
    fireEvent.click(screen.getByRole('checkbox', { name: 'Add Chat B to grid' }));

    expect(gridButton).toBeEnabled();
    fireEvent.click(gridButton);

    // The grid mounts with one rich (ChatViewHost) tile per selected chat.
    expect(document.querySelector('.grid-pane')).toBeInTheDocument();
    const tiles = screen.getAllByTestId('rich-tile');
    expect(tiles.map((t) => t.getAttribute('data-id'))).toEqual(['a', 'b']);
  });
});
