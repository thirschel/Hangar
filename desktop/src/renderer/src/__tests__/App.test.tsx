// @vitest-environment jsdom
import { act, render, screen } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { Response } from '../../../main/host-client';
import type { WorkspaceInfo } from '../../../main/host-client';
import type { Settings } from '../../../main/settings';
import { PROTO_VERSION } from '../../../shared/proto-version';

vi.mock('../components/Sidebar', () => ({
  Sidebar: () => <aside className="sidebar">Sidebar</aside>,
}));

vi.mock('../components/CenterPane', () => ({
  CenterPane: () => <section className="center-pane">Center pane</section>,
}));

vi.mock('../components/RightPanel', () => ({
  RightPanel: () => <aside className="right-panel">Right panel</aside>,
}));

import { App } from '../App';

const localStorageMock = {
  getItem: vi.fn(() => null),
  setItem: vi.fn(),
  removeItem: vi.fn(),
  clear: vi.fn(),
  key: vi.fn(() => null),
  length: 0,
};

beforeEach(() => {
  vi.stubGlobal('localStorage', localStorageMock);
  localStorageMock.getItem.mockReturnValue(null);
  localStorageMock.setItem.mockClear();

  window.cs.call = vi.fn(() => new Promise<Response>(() => {}));
  window.cs.getSettings = vi.fn(() => new Promise<Settings>(() => {}));
  window.cs.listWorkspaces = vi.fn(async () => []);
});

afterEach(() => {
  vi.unstubAllGlobals();
});

describe('App', () => {
  async function renderApp() {
    let result: ReturnType<typeof render> | undefined;
    await act(async () => {
      result = render(<App />);
    });
    return result!;
  }

  it('renders without crashing', async () => {
    const { container } = await renderApp();

    expect(container.querySelector('.app-shell')).toBeInTheDocument();
  });

  it('shows connecting status initially', async () => {
    await renderApp();

    expect(screen.getByText('connecting to session-host…')).toBeInTheDocument();
  });

  it('renders the sidebar section', async () => {
    const { container } = await renderApp();

    expect(container.querySelector('.sidebar')).toBeInTheDocument();
  });

  it('renders the center pane', async () => {
    const { container } = await renderApp();

    expect(container.querySelector('.center-pane')).toBeInTheDocument();
  });

  it('cycles status filter on bare f', async () => {
    await renderApp();
    localStorageMock.setItem.mockClear();

    await act(async () => {
      window.dispatchEvent(new KeyboardEvent('keydown', { key: 'f' }));
    });

    expect(localStorageMock.setItem).toHaveBeenCalledWith('cs.statusFilter', 'waiting');
  });

  it('does not write status filter for other bare keys', async () => {
    await renderApp();
    localStorageMock.setItem.mockClear();

    await act(async () => {
      window.dispatchEvent(new KeyboardEvent('keydown', { key: 'z' }));
    });

    expect(localStorageMock.setItem).not.toHaveBeenCalledWith('cs.statusFilter', expect.any(String));
  });
});

// ---------------------------------------------------------------------------
// Fix 3: dirty-check — verify real workspace changes are never suppressed
// ---------------------------------------------------------------------------

function makeWs(overrides: Partial<WorkspaceInfo> = {}): WorkspaceInfo {
  return {
    id: 'ws-a',
    title: 'Alpha',
    program: 'copilot',
    repoPath: 'C:\\repo',
    worktreePath: 'C:\\repo\\.wt',
    branch: 'main',
    sessionName: 'ws_a',
    alive: true,
    autoYes: false,
    added: 0,
    removed: 0,
    createdUnix: 0,
    lastOutputUnix: 0,
    runCommand: '',
    running: false,
    previewUrl: '',
    busy: false,
    waiting: false,
    regenerating: false,
    shell: 'pwsh',
    hasWorktree: true,
    ...overrides,
  };
}

describe('App dirty-check: workspace changes propagate', () => {
  afterEach(() => {
    vi.useRealTimers();
  });

  it('shows an added workspace in the footer count after a re-poll', async () => {
    let list: WorkspaceInfo[] = [];
    window.cs.call = vi.fn(async () => ({ hostVersion: PROTO_VERSION }) as never);
    window.cs.listWorkspaces = vi.fn(async () => list);
    vi.useFakeTimers();

    let result: ReturnType<typeof render> | undefined;
    await act(async () => {
      result = render(<App />);
      // flush Hello → refresh → listWorkspaces microtask chain
      await vi.runAllTimersAsync();
    });
    const { container } = result!;

    expect(container.querySelector('footer')?.textContent).toContain('0 workspaces');

    // Add a workspace, advance past the 2 s poll interval, flush microtasks.
    list = [makeWs()];
    await act(async () => {
      await vi.advanceTimersByTimeAsync(2001);
    });

    // State is updated within the act-wrapped timer advance; assert directly.
    expect(container.querySelector('footer')?.textContent).toContain('1 workspace');
  });

  it('removes a deleted workspace from the footer count after a re-poll', async () => {
    let list: WorkspaceInfo[] = [makeWs()];
    window.cs.call = vi.fn(async () => ({ hostVersion: PROTO_VERSION }) as never);
    window.cs.listWorkspaces = vi.fn(async () => list);
    vi.useFakeTimers();

    let result: ReturnType<typeof render> | undefined;
    await act(async () => {
      result = render(<App />);
      await vi.runAllTimersAsync();
    });
    const { container } = result!;

    expect(container.querySelector('footer')?.textContent).toContain('1 workspace');

    list = [];
    await act(async () => {
      await vi.advanceTimersByTimeAsync(2001);
    });

    // State is updated within the act-wrapped timer advance; assert directly.
    expect(container.querySelector('footer')?.textContent).toContain('0 workspaces');
  });
});
