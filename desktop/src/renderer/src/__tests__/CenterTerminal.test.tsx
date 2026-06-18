// @vitest-environment jsdom
import { act, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { WorkspaceInfo } from '../../../main/host-client';
import type { Settings } from '../../../main/settings';

// Mock ShellTerminal so the dock tests don't pull in xterm. forwardRef keeps the
// ref CenterTerminal passes from triggering a React warning.
vi.mock('../components/ShellTerminal', async () => {
  const { forwardRef } = await import('react');
  return {
    ShellTerminal: forwardRef(function ShellTerminal() {
      return <div data-testid="shell">shell</div>;
    }),
  };
});

import { CenterTerminal } from '../components/CenterTerminal';

const ws = { id: 'ws1', worktreePath: 'C:\\wt' } as WorkspaceInfo;

const baseSettings: Settings = {
  defaultProgram: 'copilot',
  defaultShell: 'cmd',
  autoYes: false,
  branchPrefix: '',
  workspaceDir: '',
  notifications: true,
  notificationSound: true,
  minimizeToTray: true,
  uiRefreshMs: 2000,
  autoUpdate: false,
  terminalProfiles: [
    { id: 'pwsh', label: 'PowerShell 7', command: 'pwsh.exe', args: ['-NoLogo'] },
    { id: 'cmd', label: 'Command Prompt', command: 'cmd.exe' },
  ],
  defaultTerminalProfileId: 'pwsh',
};

const localStorageMock = {
  getItem: vi.fn(() => null),
  setItem: vi.fn(),
  removeItem: vi.fn(),
  clear: vi.fn(),
  key: vi.fn(() => null),
  length: 0,
};

beforeEach(() => {
  vi.clearAllMocks();
  vi.stubGlobal('localStorage', localStorageMock);
  window.cs.getSettings = vi.fn(async () => baseSettings);
});

afterEach(() => {
  vi.unstubAllGlobals();
});

describe('CenterTerminal', () => {
  it('is collapsed by default with no shell instance', async () => {
    render(<CenterTerminal workspace={ws} />);
    await screen.findByRole('combobox', { name: 'Shell' });

    expect(screen.getByTitle('Open terminal')).toBeInTheDocument();
    expect(screen.queryByTestId('shell')).toBeNull();
    expect(screen.queryByRole('separator')).toBeNull();
  });

  it('opens the terminal with a shell and a resizer when toggled', async () => {
    render(<CenterTerminal workspace={ws} />);
    await screen.findByRole('combobox', { name: 'Shell' });

    await act(async () => {
      fireEvent.click(screen.getByTitle('Open terminal'));
    });

    expect(screen.getByTestId('shell')).toBeInTheDocument();
    expect(screen.getByRole('separator', { name: 'Resize terminal' })).toBeInTheDocument();
    expect(screen.getByTitle('Collapse terminal')).toBeInTheDocument();
  });

  it('kills the terminal via closeShell and collapses', async () => {
    const closeShell = vi.fn(async () => {});
    window.cs.closeShell = closeShell;

    render(<CenterTerminal workspace={ws} />);
    await screen.findByRole('combobox', { name: 'Shell' });
    await act(async () => {
      fireEvent.click(screen.getByTitle('Open terminal'));
    });
    expect(screen.getByTestId('shell')).toBeInTheDocument();

    await act(async () => {
      fireEvent.click(screen.getByTitle('Close terminal'));
    });

    expect(closeShell).toHaveBeenCalledWith('ws1');
    expect(screen.queryByTestId('shell')).toBeNull();
  });

  it('renders shell profile options and defaults to the configured profile', async () => {
    render(<CenterTerminal workspace={ws} />);

    const select = await screen.findByRole<HTMLSelectElement>('combobox', { name: 'Shell' });

    expect(screen.getByRole('option', { name: 'PowerShell 7' })).toBeInTheDocument();
    expect(screen.getByRole('option', { name: 'Command Prompt' })).toBeInTheDocument();
    expect(select.value).toBe('pwsh');
  });
});
