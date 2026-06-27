// @vitest-environment jsdom
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { Response } from '../../../main/host-client';
import type { WorkspaceInfo } from '../../../main/host-client';
import type { Settings } from '../../../main/settings';
import { PROTO_VERSION } from '../../../shared/proto-version';

// Keep the standard-mode workspace grid lightweight: these panes pull in
// terminal/stream machinery this suite does not exercise. The shared Sidebar is
// stubbed too (the agent surface now reuses it); AgentMode is left unmocked so
// we can assert the real `.app-mode-agent` surface the toggle swaps in.
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

// Matches the persistence key used by App.tsx for the global app-mode toggle.
const APP_MODE_KEY = 'cs.appMode';

// A small but functional in-memory Storage. We stub `localStorage` with this
// (like App.test.tsx) instead of using the environment's real storage so that
// (a) the toggle's persist + restore-on-mount paths run deterministically and
// (b) this file never touches the process-wide storage shared across test files.
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
  // Fresh storage per test so cases do not leak into one another.
  vi.stubGlobal('localStorage', createStorageMock());

  // Mirror App.test.tsx: keep the daemon connect effect pending so the app stays
  // in its initial state while we exercise the app-mode toggle. The mode UI
  // renders independently of connection state.
  window.cs.call = vi.fn(() => new Promise<Response>(() => {}));
  window.cs.getSettings = vi.fn(() => new Promise<Settings>(() => {}));
  window.cs.listWorkspaces = vi.fn(async () => []);
});

afterEach(() => {
  vi.unstubAllGlobals();
});

async function renderApp(): Promise<ReturnType<typeof render>> {
  let result: ReturnType<typeof render> | undefined;
  await act(async () => {
    result = render(<App />);
  });
  return result!;
}

// The top-bar app-mode toggle. Its accessible name comes from aria-label, so it
// is reachable by the same query regardless of the current mode.
function modeToggle(): HTMLElement {
  return screen.getByRole('button', { name: /toggle app mode/i });
}

describe('App app-mode toggle', () => {
  it('defaults to standard mode with empty localStorage', async () => {
    const { container } = await renderApp();

    // Standard workspace grid is present; the agent surface is not.
    expect(container.querySelector('main.workspace')).toBeInTheDocument();
    expect(container.querySelector('.sidebar')).toBeInTheDocument();
    expect(container.querySelector('.app-mode-agent')).not.toBeInTheDocument();
    expect(modeToggle()).toHaveAttribute('aria-pressed', 'false');
  });

  it('switches to agent mode when the top-bar mode button is clicked', async () => {
    const { container } = await renderApp();

    fireEvent.click(modeToggle());

    // Agent surface mounts and the standard workspace grid is gone. The shared
    // Sidebar (stubbed) is reused inside the agent surface, so `.sidebar` stays.
    expect(container.querySelector('.app-mode-agent')).toBeInTheDocument();
    expect(container.querySelector('main.workspace')).not.toBeInTheDocument();
    expect(container.querySelector('.sidebar')).toBeInTheDocument();

    // The agent main pane shows its empty state until a chat is selected.
    expect(screen.getByText(/select a chat or start a new one/i)).toBeInTheDocument();
    expect(modeToggle()).toHaveAttribute('aria-pressed', 'true');
  });

  it('persists the selected mode to localStorage', async () => {
    await renderApp();

    // Nothing is written until the user toggles.
    expect(localStorage.getItem(APP_MODE_KEY)).toBeNull();

    fireEvent.click(modeToggle());
    expect(localStorage.getItem(APP_MODE_KEY)).toBe('agent');

    fireEvent.click(modeToggle());
    expect(localStorage.getItem(APP_MODE_KEY)).toBe('standard');
  });

  it('restores agent mode from localStorage on mount', async () => {
    localStorage.setItem(APP_MODE_KEY, 'agent');

    const { container } = await renderApp();

    expect(container.querySelector('.app-mode-agent')).toBeInTheDocument();
    expect(container.querySelector('main.workspace')).not.toBeInTheDocument();
    expect(screen.getByText(/select a chat or start a new one/i)).toBeInTheDocument();
    expect(modeToggle()).toHaveAttribute('aria-pressed', 'true');
  });

  it('keeps the mode toggle reachable in both modes', async () => {
    await renderApp();

    // Reachable in the default standard mode.
    expect(modeToggle()).toBeInTheDocument();

    fireEvent.click(modeToggle());

    // Still reachable after switching to agent mode.
    expect(modeToggle()).toBeInTheDocument();
  });
});

// ---------------------------------------------------------------------------
// Fix 5: selection normalisation on mode switch
// ---------------------------------------------------------------------------

function makeWorkspace(overrides: Partial<WorkspaceInfo> = {}): WorkspaceInfo {
  return {
    id: 'ws-a',
    title: 'Standard WS',
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

describe('App mode-switch selection normalisation', () => {
  // Override the connection mock so workspaces actually load in these tests.
  beforeEach(() => {
    window.cs.call = vi.fn(async () => ({ hostVersion: PROTO_VERSION }) as never);
    window.cs.listWorkspaces = vi.fn(async () => []);
  });

  it('clears selectedId when switching to agent mode if selected workspace is not rich', async () => {
    const std = makeWorkspace({ id: 'ws-std', title: 'Standard WS' }); // no kind → not rich
    window.cs.listWorkspaces = vi.fn(async () => [std]);

    let focusCb: ((id: string) => void) | undefined;
    window.cs.onFocusWorkspace = vi.fn((cb: (id: string) => void) => {
      focusCb = cb;
      return () => {};
    });

    const { container } = await renderApp();
    // Wait for the workspace to load (footer count changes from 0 to 1)
    await waitFor(() => {
      expect(container.querySelector('footer')?.textContent).toContain('1 workspace');
    });

    // Select the standard workspace via the focus callback
    await act(async () => { focusCb?.('ws-std'); });
    expect(container.querySelector('.breadcrumb')?.textContent).toContain('Standard WS');

    // Switch to agent mode — standard workspace is not rich so selection must clear
    await act(async () => { fireEvent.click(modeToggle()); });
    expect(container.querySelector('.breadcrumb')?.textContent).not.toContain('Standard WS');
    // Breadcrumb falls back to the placeholder text when nothing is selected
    expect(container.querySelector('.breadcrumb')?.textContent).toContain('Workspaces');
  });

  it('keeps selectedId when switching to agent mode if the selected workspace is rich', async () => {
    const rich = makeWorkspace({ id: 'ws-rich', title: 'Rich Chat', kind: 'rich' });
    window.cs.listWorkspaces = vi.fn(async () => [rich]);

    let focusCb: ((id: string) => void) | undefined;
    window.cs.onFocusWorkspace = vi.fn((cb: (id: string) => void) => {
      focusCb = cb;
      return () => {};
    });

    const { container } = await renderApp();
    await waitFor(() => {
      expect(container.querySelector('footer')?.textContent).toContain('1 workspace');
    });

    await act(async () => { focusCb?.('ws-rich'); });
    expect(container.querySelector('.breadcrumb')?.textContent).toContain('Rich Chat');

    // Switch to agent mode — rich workspace IS visible in agent mode, keep selection
    await act(async () => { fireEvent.click(modeToggle()); });
    expect(container.querySelector('.breadcrumb')?.textContent).toContain('Rich Chat');
  });

  it('keeps selectedId when switching back to standard mode', async () => {
    // Start in agent mode with a rich workspace selected
    localStorage.setItem('cs.appMode', 'agent');
    const rich = makeWorkspace({ id: 'ws-rich', title: 'Rich Chat', kind: 'rich' });
    window.cs.listWorkspaces = vi.fn(async () => [rich]);

    let focusCb: ((id: string) => void) | undefined;
    window.cs.onFocusWorkspace = vi.fn((cb: (id: string) => void) => {
      focusCb = cb;
      return () => {};
    });

    const { container } = await renderApp();
    await waitFor(() => {
      expect(container.querySelector('footer')?.textContent).toContain('1 workspace');
    });

    await act(async () => { focusCb?.('ws-rich'); });
    expect(container.querySelector('.breadcrumb')?.textContent).toContain('Rich Chat');

    // Switch back to standard mode — rich workspace is visible in standard mode too
    await act(async () => { fireEvent.click(modeToggle()); });
    expect(container.querySelector('.breadcrumb')?.textContent).toContain('Rich Chat');
  });
});
