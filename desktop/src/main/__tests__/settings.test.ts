import path from 'node:path';
import { beforeEach, describe, expect, it, vi } from 'vitest';

const osMock = vi.hoisted(() => ({
  homedir: 'C:\\Users\\tester',
}));

const fsMock = vi.hoisted(() => {
  const files = new Map<string, string>();
  return {
    files,
    readFileSync: vi.fn((file: string) => {
      const content = files.get(String(file));
      if (content === undefined) {
        const error = new Error(`ENOENT: ${file}`);
        Object.assign(error, { code: 'ENOENT' });
        throw error;
      }
      return content;
    }),
    writeFileSync: vi.fn((file: string, content: string) => {
      files.set(String(file), String(content));
    }),
    mkdirSync: vi.fn(),
  };
});

vi.mock('node:fs', () => ({
  readFileSync: fsMock.readFileSync,
  writeFileSync: fsMock.writeFileSync,
  mkdirSync: fsMock.mkdirSync,
}));

vi.mock('node:os', () => ({
  default: { homedir: () => osMock.homedir },
  homedir: () => osMock.homedir,
}));

import { applySettings, getSettings } from '../settings';

const configPath = path.join(osMock.homedir, '.hangar', 'config.json');
const appSettingsPath = path.join(osMock.homedir, '.hangar', 'desktop.json');

function readWrittenJson(file: string): Record<string, unknown> {
  return JSON.parse(fsMock.files.get(file) ?? '{}') as Record<string, unknown>;
}

describe('settings', () => {
  beforeEach(() => {
    fsMock.files.clear();
    vi.clearAllMocks();
  });

  it('getSettings returns defaults when no config files exist', () => {
    expect(getSettings()).toEqual({
      defaultProgram: 'copilot',
      defaultShell: 'cmd',
      autoYes: false,
      branchPrefix: '',
      workspaceDir: '',
      notifications: true,
      minimizeToTray: true,
      autoUpdate: false,
      uiRefreshMs: 2000,
    });
  });

  it('getSettings returns autoUpdate default of false', () => {
    const missingFile = () => {
      const error = new Error('ENOENT');
      Object.assign(error, { code: 'ENOENT' });
      throw error;
    };
    fsMock.readFileSync.mockImplementationOnce(missingFile).mockImplementationOnce(missingFile);

    expect(getSettings().autoUpdate).toBe(false);
  });

  it('applySettings merges daemon and app config correctly', () => {
    fsMock.files.set(
      configPath,
      JSON.stringify({ default_program: 'claude', auto_yes: true, branch_prefix: 'old/' }),
    );
    fsMock.files.set(
      appSettingsPath,
      JSON.stringify({ notifications: false, minimizeToTray: false, uiRefreshMs: 1500 }),
    );

    const result = applySettings({
      defaultProgram: '  copilot  ',
      defaultShell: 'powershell',
      autoYes: false,
      branchPrefix: 'feature/',
      workspaceDir: '  C:\\worktrees  ',
      notifications: true,
      minimizeToTray: true,
      autoUpdate: false,
      uiRefreshMs: 2500,
    });

    expect(fsMock.mkdirSync).toHaveBeenCalledWith(path.join(osMock.homedir, '.hangar'), {
      recursive: true,
    });
    expect(readWrittenJson(configPath)).toEqual({
      default_program: 'copilot',
      auto_yes: false,
      branch_prefix: 'feature/',
      default_shell: 'powershell',
      worktree_dir: 'C:\\worktrees',
    });
    expect(readWrittenJson(appSettingsPath)).toEqual({
      notifications: true,
      minimizeToTray: true,
      autoUpdate: false,
      uiRefreshMs: 2500,
      setupComplete: false,
    });
    expect(result).toEqual({
      defaultProgram: 'copilot',
      defaultShell: 'powershell',
      autoYes: false,
      branchPrefix: 'feature/',
      workspaceDir: 'C:\\worktrees',
      notifications: true,
      minimizeToTray: true,
      autoUpdate: false,
      uiRefreshMs: 2500,
    });
  });

  it('applySettings persists autoUpdate flag', () => {
    applySettings({ autoUpdate: true });

    expect(readWrittenJson(appSettingsPath)).toEqual({
      notifications: true,
      minimizeToTray: true,
      autoUpdate: true,
      uiRefreshMs: 2000,
      setupComplete: false,
    });
  });

  it('applySettings preserves unknown daemon keys', () => {
    fsMock.files.set(
      configPath,
      JSON.stringify({
        daemon_poll_interval: 3000,
        custom_flag: true,
      }),
    );

    applySettings({ branchPrefix: 'feature/' });

    expect(readWrittenJson(configPath)).toEqual({
      daemon_poll_interval: 3000,
      custom_flag: true,
      branch_prefix: 'feature/',
    });
  });

  it('applySettings clamps uiRefreshMs to bounds', () => {
    const low = applySettings({ uiRefreshMs: 100 });
    expect(low.uiRefreshMs).toBe(500);
    expect(readWrittenJson(appSettingsPath).uiRefreshMs).toBe(500);

    const high = applySettings({ uiRefreshMs: 100_000 });
    expect(high.uiRefreshMs).toBe(60000);
    expect(readWrittenJson(appSettingsPath).uiRefreshMs).toBe(60000);
  });

  it("applySettings trims defaultProgram and falls back to 'copilot' if empty", () => {
    const trimmed = applySettings({ defaultProgram: '  code  ' });
    expect(trimmed.defaultProgram).toBe('code');
    expect(readWrittenJson(configPath).default_program).toBe('code');

    const fallback = applySettings({ defaultProgram: '   ' });
    expect(fallback.defaultProgram).toBe('copilot');
    expect(readWrittenJson(configPath).default_program).toBe('copilot');
  });
});
