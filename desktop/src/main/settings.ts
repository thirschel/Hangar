import { readFileSync, writeFileSync, mkdirSync } from 'node:fs';
import path from 'node:path';
import os from 'node:os';
import { execFileSync } from 'node:child_process';

// The daemon's config (read by the Go core-daemon). We preserve unknown keys on
// write so the daemon's own fields aren't clobbered.
export type DaemonConfig = {
  default_program?: string;
  auto_yes?: boolean;
  daemon_poll_interval?: number;
  branch_prefix?: string;
  worktree_dir?: string;
  default_shell?: string;
  [key: string]: unknown;
};

// Desktop-only settings, kept in a separate file so writing them never drops the
// daemon's config keys (the daemon rewrites config.json and would lose unknowns).
export type AppSettings = {
  notifications: boolean;
  notificationSound: boolean;
  minimizeToTray: boolean;
  uiRefreshMs: number;
  autoUpdate: boolean;
  setupComplete: boolean;
  terminalProfiles: ShellProfile[];
  defaultTerminalProfileId: string;
};

export type ShellProfile = { id: string; label: string; command: string; args?: string[] };

// The merged, flat view the renderer's Settings UI works with.
export type Settings = {
  defaultProgram: string;
  defaultShell: string;
  autoYes: boolean;
  branchPrefix: string;
  workspaceDir: string;
  notifications: boolean;
  notificationSound: boolean;
  minimizeToTray: boolean;
  uiRefreshMs: number;
  autoUpdate: boolean;
  terminalProfiles: ShellProfile[];
  defaultTerminalProfileId: string;
};

const APP_DEFAULTS: AppSettings = {
  notifications: true,
  notificationSound: true,
  minimizeToTray: true,
  uiRefreshMs: 2000,
  autoUpdate: false,
  setupComplete: false,
  terminalProfiles: [],
  defaultTerminalProfileId: '',
};

function csDir(): string {
  return path.join(os.homedir(), '.hangar');
}

function configPath(): string {
  return path.join(csDir(), 'config.json');
}

function appSettingsPath(): string {
  return path.join(csDir(), 'desktop.json');
}

function readJson<T>(file: string, fallback: T): T {
  try {
    return { ...fallback, ...(JSON.parse(readFileSync(file, 'utf8')) as object) } as T;
  } catch {
    return fallback;
  }
}

export function readDaemonConfig(): DaemonConfig {
  return readJson<DaemonConfig>(configPath(), {});
}

function commandExists(command: string): boolean {
  try {
    execFileSync('where.exe', [command], { stdio: 'ignore', windowsHide: true });
    return true;
  } catch {
    return false;
  }
}

function fileExists(file: string): boolean {
  try {
    execFileSync('cmd.exe', ['/d', '/s', '/c', `if exist "${file}" (exit /b 0) else (exit /b 1)`], {
      stdio: 'ignore',
      windowsHide: true,
    });
    return true;
  } catch {
    return false;
  }
}

export function detectShells(): ShellProfile[] {
  const profiles: ShellProfile[] = [];
  if (commandExists('pwsh.exe')) {
    profiles.push({ id: 'pwsh', label: 'PowerShell 7', command: 'pwsh.exe', args: ['-NoLogo'] });
  }
  if (commandExists('powershell.exe')) {
    profiles.push({
      id: 'powershell',
      label: 'Windows PowerShell',
      command: 'powershell.exe',
      args: ['-NoLogo'],
    });
  }
  if (commandExists('cmd.exe')) {
    profiles.push({ id: 'cmd', label: 'Command Prompt', command: 'cmd.exe' });
  }
  if (commandExists('wsl.exe')) {
    profiles.push({ id: 'wsl', label: 'WSL', command: 'wsl.exe' });
  }

  const gitBashPaths = [
    'C:\\Program Files\\Git\\bin\\bash.exe',
    'C:\\Program Files (x86)\\Git\\bin\\bash.exe',
  ];
  const gitBash = gitBashPaths.find((candidate) => fileExists(candidate));
  if (gitBash) {
    profiles.push({ id: 'git-bash', label: 'Git Bash', command: gitBash });
  }

  return profiles;
}

function defaultTerminalProfileId(profiles: ShellProfile[]): string {
  if (profiles.some((profile) => profile.id === 'pwsh')) return 'pwsh';
  if (profiles.some((profile) => profile.id === 'powershell')) return 'powershell';
  return 'cmd';
}

function seedTerminalSettings(app: AppSettings): AppSettings {
  const needsProfiles = !Array.isArray(app.terminalProfiles) || app.terminalProfiles.length === 0;
  if (needsProfiles) {
    app.terminalProfiles = detectShells();
    if (!app.terminalProfiles.some((profile) => profile.id === 'cmd')) {
      app.terminalProfiles.push({ id: 'cmd', label: 'Command Prompt', command: 'cmd.exe' });
    }
  }
  const hasDefault = app.terminalProfiles.some((profile) => profile.id === app.defaultTerminalProfileId);
  if (!app.defaultTerminalProfileId || !hasDefault) {
    app.defaultTerminalProfileId = defaultTerminalProfileId(app.terminalProfiles);
  }
  return app;
}

export function isFirstRun(): boolean {
  return !readAppSettings().setupComplete;
}

export function markSetupComplete(): void {
  mkdirSync(csDir(), { recursive: true });
  const app = readAppSettings();
  app.setupComplete = true;
  writeFileSync(appSettingsPath(), JSON.stringify(app, null, 2) + '\n');
}

export function readAppSettings(): AppSettings {
  const app = readJson<AppSettings>(appSettingsPath(), { ...APP_DEFAULTS });
  const needsProfiles = !Array.isArray(app.terminalProfiles) || app.terminalProfiles.length === 0;
  const needsDefault =
    !app.defaultTerminalProfileId ||
    !app.terminalProfiles?.some((profile) => profile.id === app.defaultTerminalProfileId);
  seedTerminalSettings(app);
  if (needsProfiles || needsDefault) {
    mkdirSync(csDir(), { recursive: true, mode: 0o700 });
    writeFileSync(appSettingsPath(), JSON.stringify(app, null, 2) + '\n', { mode: 0o600 });
  }
  return app;
}

export function getSettings(): Settings {
  const cfg = readDaemonConfig();
  const app = readAppSettings();
  return {
    defaultProgram: (cfg.default_program as string) || 'copilot',
    defaultShell: (cfg.default_shell as string) || 'cmd',
    autoYes: Boolean(cfg.auto_yes),
    branchPrefix: (cfg.branch_prefix as string) || '',
    workspaceDir: (cfg.worktree_dir as string) || '',
    notifications: app.notifications,
    notificationSound: app.notificationSound,
    minimizeToTray: app.minimizeToTray,
    uiRefreshMs: app.uiRefreshMs,
    autoUpdate: app.autoUpdate,
    terminalProfiles: app.terminalProfiles,
    defaultTerminalProfileId: app.defaultTerminalProfileId,
  };
}

// applySettings merges a partial Settings patch back into the two stores. Daemon
// fields go to config.json (preserving its other keys); app fields to desktop.json.
export function applySettings(patch: Partial<Settings>): Settings {
  mkdirSync(csDir(), { recursive: true, mode: 0o700 });

  const cfg = readDaemonConfig();
  if (patch.defaultProgram !== undefined)
    cfg.default_program = patch.defaultProgram.trim() || 'copilot';
  if (patch.defaultShell !== undefined)
    cfg.default_shell = patch.defaultShell || 'cmd';
  if (patch.autoYes !== undefined) cfg.auto_yes = patch.autoYes;
  if (patch.branchPrefix !== undefined) cfg.branch_prefix = patch.branchPrefix;
  if (patch.workspaceDir !== undefined) {
    const dir = patch.workspaceDir.trim();
    if (dir) cfg.worktree_dir = dir;
    else delete cfg.worktree_dir;
  }
  writeFileSync(configPath(), JSON.stringify(cfg, null, 2) + '\n', { mode: 0o600 });

  const app = readAppSettings();
  if (patch.notifications !== undefined) app.notifications = patch.notifications;
  if (patch.notificationSound !== undefined) app.notificationSound = patch.notificationSound;
  if (patch.minimizeToTray !== undefined) app.minimizeToTray = patch.minimizeToTray;
  if (patch.autoUpdate !== undefined) app.autoUpdate = patch.autoUpdate;
  if (patch.terminalProfiles !== undefined) app.terminalProfiles = patch.terminalProfiles;
  if (patch.defaultTerminalProfileId !== undefined) app.defaultTerminalProfileId = patch.defaultTerminalProfileId;
  if (patch.uiRefreshMs !== undefined) {
    const n = Number(patch.uiRefreshMs);
    app.uiRefreshMs = Number.isFinite(n)
      ? Math.min(60000, Math.max(500, n))
      : APP_DEFAULTS.uiRefreshMs;
  }
  writeFileSync(appSettingsPath(), JSON.stringify(app, null, 2) + '\n', { mode: 0o600 });

  return getSettings();
}
