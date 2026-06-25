// @vitest-environment jsdom
import { act, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { Response } from '../../../main/host-client';
import type { Settings } from '../../../main/settings';

// Keep the standard-mode workspace grid lightweight: these panes pull in
// terminal/stream machinery this suite does not exercise. The agent-mode shell
// (AgentMode + ChatSidebar) is left unmocked so we can assert the real
// `.app-mode-agent` surface the toggle swaps in.
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

    // Agent surface mounts and the standard workspace grid is gone.
    expect(container.querySelector('.app-mode-agent')).toBeInTheDocument();
    expect(container.querySelector('main.workspace')).not.toBeInTheDocument();
    expect(container.querySelector('.sidebar')).not.toBeInTheDocument();

    // The agent shell shows the Chats list and its empty state.
    expect(screen.getByRole('navigation', { name: 'Chats' })).toBeInTheDocument();
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
