// @vitest-environment jsdom
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { Settings, ShellProfile } from '../../../main/settings';
import { SettingsModal } from '../components/SettingsModal';

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

const detectedShells: ShellProfile[] = [
  { id: 'powershell', label: 'Windows PowerShell', command: 'powershell.exe', args: ['-NoLogo'] },
];

describe('SettingsModal', () => {
  beforeEach(() => {
    window.cs.getSettings = vi.fn(async () => baseSettings);
    window.cs.setSettings = vi.fn(async (settings: Partial<Settings>) => settings as Settings);
    window.cs.detectShells = vi.fn(async () => detectedShells);
  });

  it('renders with General and Terminal tabs', async () => {
    render(<SettingsModal onClose={vi.fn()} />);

    expect(await screen.findByRole('tab', { name: 'General' })).toHaveAttribute(
      'aria-selected',
      'true',
    );
    expect(screen.getByRole('tab', { name: 'Terminal' })).toHaveAttribute(
      'aria-selected',
      'false',
    );
    expect(screen.getByText('Default agent')).toBeInTheDocument();
  });

  it('shows terminal profile rows when the Terminal tab is selected', async () => {
    render(<SettingsModal onClose={vi.fn()} />);

    fireEvent.click(await screen.findByRole('tab', { name: 'Terminal' }));

    expect(screen.getByRole('tab', { name: 'Terminal' })).toHaveAttribute('aria-selected', 'true');
    expect(screen.getByDisplayValue('PowerShell 7')).toBeInTheDocument();
    expect(screen.getByDisplayValue('Command Prompt')).toBeInTheDocument();
    expect(screen.getByLabelText('PowerShell 7 command')).toHaveValue('pwsh.exe');
  });

  it('adds a new terminal profile row', async () => {
    render(<SettingsModal onClose={vi.fn()} />);

    fireEvent.click(await screen.findByRole('tab', { name: 'Terminal' }));
    fireEvent.click(screen.getByRole('button', { name: 'Add profile' }));

    expect(screen.getByDisplayValue('Custom shell')).toBeInTheDocument();
  });

  it('auto-detects installed terminal profiles', async () => {
    render(<SettingsModal onClose={vi.fn()} />);

    fireEvent.click(await screen.findByRole('tab', { name: 'Terminal' }));
    fireEvent.click(screen.getByRole('button', { name: 'Auto-detect' }));

    await waitFor(() => expect(window.cs.detectShells).toHaveBeenCalledTimes(1));
    expect(await screen.findByDisplayValue('Windows PowerShell')).toBeInTheDocument();
  });
});
