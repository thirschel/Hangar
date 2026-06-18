// @vitest-environment jsdom
import { act, render, screen } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { Response } from '../../../main/host-client';
import type { Settings } from '../../../main/settings';

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
