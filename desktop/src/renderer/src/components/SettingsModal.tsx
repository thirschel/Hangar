import type { JSX } from 'react';
import { useCallback, useEffect, useRef, useState } from 'react';
import { Modal, type ModalHandle } from './Modal';
import type { Settings, ShellProfile } from '../../../main/settings';
import type { LogContent, LogPaths, LogWhich } from '../../../preload';

type AppInfo = {
  version: string;
  appName: string;
  electronVersion: string;
  nodeVersion: string;
  platform: string;
  arch: string;
  githubUrl: string;
  author: string;
};

type UpdateStatusInfo = {
  status: string;
  version?: string;
  progress?: number;
  error?: string;
};

type SettingsTab = 'general' | 'terminal' | 'diagnostics';

type CsApiWithUpdates = typeof window.cs & {
  getAppInfo?: () => Promise<AppInfo>;
  checkForUpdate?: () => Promise<UpdateStatusInfo>;
  downloadUpdate?: () => Promise<void>;
  installUpdate?: () => Promise<void>;
  onUpdateStatus?: (callback: (status: UpdateStatusInfo) => void) => (() => void) | void;
};

function getCsApi(): CsApiWithUpdates {
  return window.cs as CsApiWithUpdates;
}

type SettingsModalProps = {
  onClose: () => void;
  onSaved?: (next: Settings) => void;
};

function splitArgs(value: string): string[] | undefined {
  const args = value.split(/\s+/).filter(Boolean);
  return args.length > 0 ? args : undefined;
}

function normalizeTerminalDefaults(
  terminalProfiles: ShellProfile[],
  defaultTerminalProfileId: string,
): Pick<Settings, 'terminalProfiles' | 'defaultTerminalProfileId'> {
  const hasDefault = terminalProfiles.some((profile) => profile.id === defaultTerminalProfileId);
  return {
    terminalProfiles,
    defaultTerminalProfileId: hasDefault ? defaultTerminalProfileId : (terminalProfiles[0]?.id ?? ''),
  };
}

function profileInputId(profileId: string, field: string): string {
  return `terminal-profile-${profileId}-${field}`;
}

export function SettingsModal({ onClose, onSaved }: SettingsModalProps): JSX.Element {
  const [settings, setSettings] = useState<Settings | null>(null);
  const [activeTab, setActiveTab] = useState<SettingsTab>('general');
  const [busy, setBusy] = useState(false);
  const [detectingShells, setDetectingShells] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [appInfo, setAppInfo] = useState<AppInfo | null>(null);
  const [updateStatus, setUpdateStatus] = useState<string>('idle');
  const [updateVersion, setUpdateVersion] = useState<string | null>(null);
  const [updateProgress, setUpdateProgress] = useState<number | null>(null);
  const [updateError, setUpdateError] = useState<string | null>(null);
  const modalRef = useRef<ModalHandle>(null);

  useEffect(() => {
    let active = true;
    window.cs
      .getSettings()
      .then((v) => {
        if (active) setSettings(v);
      })
      .catch((e) => setError(e instanceof Error ? e.message : String(e)));
    return () => {
      active = false;
    };
  }, []);

  useEffect(() => {
    const cs = getCsApi();
    cs.getAppInfo
      ?.()
      .then((info: AppInfo) => setAppInfo(info))
      .catch(() => {});

    const unsub = cs.onUpdateStatus?.((status: UpdateStatusInfo) => {
      setUpdateStatus(status.status);
      if (status.version) setUpdateVersion(status.version);
      if (status.progress != null) setUpdateProgress(status.progress);
      if (status.error) setUpdateError(status.error);
    });
    return () => unsub?.();
  }, []);

  const patch = (p: Partial<Settings>): void => setSettings((s) => (s ? { ...s, ...p } : s));

  const patchTerminalProfiles = (
    terminalProfiles: ShellProfile[],
    defaultTerminalProfileId = settings?.defaultTerminalProfileId ?? '',
  ): void => {
    patch(normalizeTerminalDefaults(terminalProfiles, defaultTerminalProfileId));
  };

  const browseWorkspaceDir = async (): Promise<void> => {
    const dir = await window.cs.pickFolder();
    if (dir) patch({ workspaceDir: dir });
  };

  const save = async (): Promise<void> => {
    if (!settings) return;
    setBusy(true);
    setError(null);
    try {
      const next = await window.cs.setSettings(settings);
      onSaved?.(next);
      modalRef.current?.close();
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  };

  const handleCheckForUpdate = async (): Promise<void> => {
    const cs = getCsApi();
    setUpdateStatus('checking');
    setUpdateError(null);
    try {
      const result = (await cs.checkForUpdate?.()) as UpdateStatusInfo | undefined;
      if (result) {
        setUpdateStatus(result.status);
        if (result.version) setUpdateVersion(result.version);
        if (result.error) setUpdateError(result.error);
      }
    } catch (e) {
      setUpdateStatus('error');
      setUpdateError(e instanceof Error ? e.message : String(e));
    }
  };

  const handleDownloadUpdate = async (): Promise<void> => {
    const cs = getCsApi();
    setUpdateStatus('downloading');
    try {
      await cs.downloadUpdate?.();
    } catch (e) {
      setUpdateStatus('error');
      setUpdateError(e instanceof Error ? e.message : String(e));
    }
  };

  const handleInstallUpdate = async (): Promise<void> => {
    await getCsApi().installUpdate?.();
  };

  const addTerminalProfile = (): void => {
    if (!settings) return;
    const id =
      typeof crypto !== 'undefined' && 'randomUUID' in crypto
        ? `custom-${crypto.randomUUID()}`
        : `custom-${Date.now()}-${Math.random().toString(36).slice(2)}`;
    const nextProfiles = [
      ...settings.terminalProfiles,
      { id, label: 'Custom shell', command: '', args: undefined },
    ];
    patchTerminalProfiles(nextProfiles, settings.defaultTerminalProfileId || id);
  };

  const updateTerminalProfile = (id: string, partial: Partial<ShellProfile>): void => {
    if (!settings) return;
    patchTerminalProfiles(
      settings.terminalProfiles.map((profile) =>
        profile.id === id ? { ...profile, ...partial } : profile,
      ),
    );
  };

  const removeTerminalProfile = (id: string): void => {
    if (!settings) return;
    const nextProfiles = settings.terminalProfiles.filter((profile) => profile.id !== id);
    patchTerminalProfiles(nextProfiles, settings.defaultTerminalProfileId);
  };

  const detectShellProfiles = async (): Promise<void> => {
    if (!settings) return;
    setDetectingShells(true);
    setError(null);
    try {
      const detected = await window.cs.detectShells();
      const existingIds = new Set(settings.terminalProfiles.map((profile) => profile.id));
      const merged = [
        ...settings.terminalProfiles,
        ...detected.filter((profile) => !existingIds.has(profile.id)),
      ];
      patchTerminalProfiles(merged, settings.defaultTerminalProfileId || merged[0]?.id || '');
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setDetectingShells(false);
    }
  };

  return (
    <Modal
      ref={modalRef}
      title="Settings"
      onClose={onClose}
      busy={busy}
      error={error}
      className="modal--settings"
      footer={
        <>
          <button type="button" onClick={() => modalRef.current?.close()} disabled={busy}>
            Cancel
          </button>
          <button
            type="button"
            className="modal__primary"
            onClick={() => void save()}
            disabled={busy || !settings}
          >
            Save
          </button>
        </>
      }
    >
      {!settings ? (
        <div>Loading…</div>
      ) : (
        <>
          <div className="settings-tabs" role="tablist" aria-label="Settings sections">
            <button
              id="settings-tab-general"
              type="button"
              role="tab"
              aria-selected={activeTab === 'general'}
              aria-controls="settings-panel-general"
              className={activeTab === 'general' ? 'settings-tab settings-tab--active' : 'settings-tab'}
              onClick={() => setActiveTab('general')}
            >
              General
            </button>
            <button
              id="settings-tab-terminal"
              type="button"
              role="tab"
              aria-selected={activeTab === 'terminal'}
              aria-controls="settings-panel-terminal"
              className={activeTab === 'terminal' ? 'settings-tab settings-tab--active' : 'settings-tab'}
              onClick={() => setActiveTab('terminal')}
            >
              Terminal
            </button>
            <button
              id="settings-tab-diagnostics"
              type="button"
              role="tab"
              aria-selected={activeTab === 'diagnostics'}
              aria-controls="settings-panel-diagnostics"
              className={
                activeTab === 'diagnostics' ? 'settings-tab settings-tab--active' : 'settings-tab'
              }
              onClick={() => setActiveTab('diagnostics')}
            >
              Diagnostics
            </button>
          </div>

          {activeTab === 'general' && (
            <div
              id="settings-panel-general"
              role="tabpanel"
              aria-labelledby="settings-tab-general"
              className="settings-tab-panel"
            >
              <label className="field">
                <span>Default agent</span>
                <input
                  value={settings.defaultProgram}
                  spellCheck={false}
                  onChange={(e) => patch({ defaultProgram: e.target.value })}
                />
              </label>
              <label className="field">
                <span>Shell</span>
                <select
                  value={settings.defaultShell}
                  onChange={(e) => patch({ defaultShell: e.target.value })}
                >
                  <option value="cmd">cmd.exe</option>
                  <option value="powershell">PowerShell 5 (powershell.exe)</option>
                  <option value="pwsh">PowerShell 7 (pwsh.exe)</option>
                </select>
              </label>
              <label className="field">
                <span>Branch prefix</span>
                <input
                  value={settings.branchPrefix}
                  spellCheck={false}
                  placeholder="username/"
                  onChange={(e) => patch({ branchPrefix: e.target.value })}
                />
              </label>
              <label className="field">
                <span>Default workspace location</span>
                <div className="field__pick">
                  <input
                    value={settings.workspaceDir}
                    spellCheck={false}
                    placeholder="~/.hangar/worktrees (default)"
                    onChange={(e) => patch({ workspaceDir: e.target.value })}
                  />
                  <button type="button" onClick={() => void browseWorkspaceDir()}>
                    Browse…
                  </button>
                </div>
              </label>
              <label className="field field--row">
                <input
                  type="checkbox"
                  checked={settings.autoYes}
                  onChange={(e) => patch({ autoYes: e.target.checked })}
                />
                <span>Default Auto-Yes for new workspaces</span>
              </label>
              <label className="field field--row">
                <input
                  type="checkbox"
                  checked={settings.notifications}
                  onChange={(e) => patch({ notifications: e.target.checked })}
                />
                <span>Desktop notifications</span>
              </label>
              <label className="field field--row">
                <input
                  type="checkbox"
                  checked={settings.notificationSound}
                  onChange={(e) => patch({ notificationSound: e.target.checked })}
                />
                <span>Notification sound</span>
              </label>
              <label className="field field--row">
                <input
                  type="checkbox"
                  checked={settings.minimizeToTray}
                  onChange={(e) => patch({ minimizeToTray: e.target.checked })}
                />
                <span>Minimize to tray on close</span>
              </label>
              <label className="field">
                <span>UI refresh interval (ms)</span>
                <input
                  type="number"
                  min={500}
                  max={60000}
                  step={500}
                  value={settings.uiRefreshMs}
                  onChange={(e) => patch({ uiRefreshMs: Number(e.target.value) })}
                />
              </label>
              <div className="settings-divider" />

              <label className="field field--row">
                <input
                  type="checkbox"
                  checked={settings.autoUpdate}
                  onChange={(e) => patch({ autoUpdate: e.target.checked })}
                />
                <span>Auto-update (download updates automatically)</span>
              </label>

              <div className="field">
                <span>Updates</span>
                <div className="update-controls">
                  <button
                    type="button"
                    className="update-btn"
                    onClick={() => void handleCheckForUpdate()}
                    disabled={updateStatus === 'checking' || updateStatus === 'downloading'}
                  >
                    {updateStatus === 'checking' ? 'Checking…' : 'Check for Updates'}
                  </button>
                  {updateVersion && updateStatus === 'available' && (
                    <button
                      type="button"
                      className="update-btn modal__primary"
                      onClick={() => void handleDownloadUpdate()}
                    >
                      Download v{updateVersion}
                    </button>
                  )}
                  {updateStatus === 'downloading' && (
                    <span className="update-progress">
                      Downloading…{updateProgress != null ? ` ${Math.round(updateProgress)}%` : ''}
                    </span>
                  )}
                  {updateStatus === 'downloaded' && updateVersion && (
                    <button
                      type="button"
                      className="update-btn modal__primary"
                      onClick={() => void handleInstallUpdate()}
                    >
                      Install v{updateVersion} & Restart
                    </button>
                  )}
                  {updateStatus === 'not-available' && (
                    <span className="update-status">Up to date</span>
                  )}
                  {updateStatus === 'error' && updateError && (
                    <span className="update-status update-status--error">{updateError}</span>
                  )}
                </div>
              </div>

              <div className="settings-divider" />

              <div className="about-section">
                <div className="about-row">
                  <span className="about-label">Version</span>
                  <span className="about-value">{appInfo?.version ?? '…'}</span>
                </div>
                <div className="about-row">
                  <span className="about-label">Electron</span>
                  <span className="about-value">
                    {appInfo ? `${appInfo.electronVersion} · Node ${appInfo.nodeVersion}` : '…'}
                  </span>
                </div>
                <div className="about-row">
                  <span className="about-label">Platform</span>
                  <span className="about-value">
                    {appInfo ? `${appInfo.platform} (${appInfo.arch})` : '…'}
                  </span>
                </div>
                <div className="about-row">
                  <span className="about-label">GitHub</span>
                  <span className="about-value">
                    <a
                      href="#"
                      onClick={(e) => {
                        e.preventDefault();
                        if (appInfo?.githubUrl) void window.cs.openExternal(appInfo.githubUrl);
                      }}
                    >
                      {appInfo?.githubUrl ?? '…'}
                    </a>
                  </span>
                </div>
                <div className="about-row">
                  <span className="about-label">Author</span>
                  <span className="about-value">{appInfo?.author ?? '…'}</span>
                </div>
              </div>
            </div>
          )}

          {activeTab === 'terminal' && (
            <div
              id="settings-panel-terminal"
              role="tabpanel"
              aria-labelledby="settings-tab-terminal"
              className="settings-tab-panel"
            >
              <div className="terminal-profiles__header">
                <div>
                  <div className="terminal-profiles__title">Terminal profiles</div>
                  <div className="terminal-profiles__hint">
                    Configure shells for the terminal dock. The selected default opens first.
                  </div>
                </div>
                <div className="terminal-profiles__actions">
                  <button type="button" onClick={addTerminalProfile}>
                    Add profile
                  </button>
                  <button
                    type="button"
                    onClick={() => void detectShellProfiles()}
                    disabled={detectingShells}
                  >
                    {detectingShells ? 'Detecting…' : 'Auto-detect'}
                  </button>
                </div>
              </div>

              <div className="terminal-profiles" aria-label="Terminal shell profiles">
                {settings.terminalProfiles.length === 0 ? (
                  <div className="terminal-profiles__empty">
                    No terminal profiles yet. Add one manually or auto-detect installed shells.
                  </div>
                ) : (
                  settings.terminalProfiles.map((profile) => (
                    <div className="terminal-profile-row" key={profile.id}>
                      <label className="terminal-profile-row__default">
                        <input
                          type="radio"
                          name="default-terminal-profile"
                          checked={settings.defaultTerminalProfileId === profile.id}
                          onChange={() => patch({ defaultTerminalProfileId: profile.id })}
                        />
                        <span>Default</span>
                      </label>
                      <label className="field terminal-profile-row__field">
                        <span>Label</span>
                        <input
                          id={profileInputId(profile.id, 'label')}
                          value={profile.label}
                          aria-label={`${profile.label || profile.id} label`}
                          spellCheck={false}
                          onChange={(e) => updateTerminalProfile(profile.id, { label: e.target.value })}
                        />
                      </label>
                      <label className="field terminal-profile-row__field terminal-profile-row__command">
                        <span>Command/exe</span>
                        <input
                          id={profileInputId(profile.id, 'command')}
                          value={profile.command}
                          aria-label={`${profile.label || profile.id} command`}
                          spellCheck={false}
                          onChange={(e) =>
                            updateTerminalProfile(profile.id, { command: e.target.value })
                          }
                        />
                      </label>
                      <label className="field terminal-profile-row__field">
                        <span>Args</span>
                        <input
                          id={profileInputId(profile.id, 'args')}
                          value={profile.args?.join(' ') ?? ''}
                          aria-label={`${profile.label || profile.id} args`}
                          spellCheck={false}
                          placeholder="-NoLogo"
                          onChange={(e) =>
                            updateTerminalProfile(profile.id, { args: splitArgs(e.target.value) })
                          }
                        />
                      </label>
                      <button
                        type="button"
                        className="terminal-profile-row__remove"
                        onClick={() => removeTerminalProfile(profile.id)}
                      >
                        Remove
                      </button>
                    </div>
                  ))
                )}
              </div>
            </div>
          )}

          {activeTab === 'diagnostics' && (
            <DiagnosticsPanel settings={settings} patch={patch} />
          )}
        </>
      )}
    </Modal>
  );
}

type DiagnosticsPanelProps = {
  settings: Settings;
  patch: (p: Partial<Settings>) => void;
};

function DiagnosticsPanel({ settings, patch }: DiagnosticsPanelProps): JSX.Element {
  const [logPaths, setLogPaths] = useState<LogPaths | null>(null);

  useEffect(() => {
    let active = true;
    window.cs
      .getLogPaths()
      .then((paths) => {
        if (active) setLogPaths(paths);
      })
      .catch(() => {});
    return () => {
      active = false;
    };
  }, []);

  return (
    <div
      id="settings-panel-diagnostics"
      role="tabpanel"
      aria-labelledby="settings-tab-diagnostics"
      className="settings-tab-panel"
    >
      <div className="diagnostics-section">
        <div className="terminal-profiles__title">Logs</div>
        <div className="diagnostics-actions">
          <button type="button" onClick={() => void window.cs.openLogFolder()}>
            Open logs folder
          </button>
          <button type="button" onClick={() => void window.cs.openLogFile('host')}>
            Open host.log
          </button>
          <button type="button" onClick={() => void window.cs.openLogFile('desktop')}>
            Open desktop.log
          </button>
          <button type="button" onClick={() => void window.cs.openLogFile('hangar')}>
            Open hangar.log
          </button>
        </div>
        {logPaths && (
          <dl className="diagnostics-paths" aria-label="Log paths">
            <div>
              <dt>host.log</dt>
              <dd>{logPaths.hostLog}</dd>
            </div>
            <div>
              <dt>desktop.log</dt>
              <dd>{logPaths.desktopLog}</dd>
            </div>
            <div>
              <dt>hangar.log</dt>
              <dd>{logPaths.hangarLog}</dd>
            </div>
          </dl>
        )}
      </div>

      <label className="field field--row diagnostics-verbose">
        <input
          type="checkbox"
          checked={settings.verboseLogging ?? false}
          onChange={(e) => patch({ verboseLogging: e.target.checked })}
        />
        <span>Verbose logging (HANGAR_DEBUG)</span>
      </label>
      <div className="diagnostics-helper">
        Logs extra detail. Takes effect after the session-host daemon restarts (e.g. restart the
        app).
      </div>

      <LogViewer />
    </div>
  );
}

function LogViewer(): JSX.Element {
  const [which, setWhich] = useState<LogWhich>('host');
  const [log, setLog] = useState<LogContent | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const refresh = useCallback(async (nextWhich: LogWhich = which): Promise<void> => {
    setLoading(true);
    setError(null);
    try {
      setLog(await window.cs.readLog(nextWhich, 65536));
    } catch (e) {
      setLog(null);
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setLoading(false);
    }
  }, [which]);

  useEffect(() => {
    void refresh(which);
  }, [refresh, which]);

  return (
    <div className="log-viewer">
      <div className="log-viewer__header">
        <label className="field log-viewer__select">
          <span>Log file</span>
          <select value={which} onChange={(e) => setWhich(e.target.value as LogWhich)}>
            <option value="host">host.log</option>
            <option value="desktop">desktop.log</option>
            <option value="hangar">hangar.log</option>
          </select>
        </label>
        <button type="button" className="update-btn" onClick={() => void refresh()} disabled={loading}>
          {loading ? 'Refreshing…' : 'Refresh'}
        </button>
      </div>
      {log && (
        <div className="log-viewer__meta">
          <span>{log.path}</span>
          {log.truncated && <strong>Showing last 64 KiB</strong>}
        </div>
      )}
      {error && <div className="log-viewer__error">{error}</div>}
      <pre className="log-viewer__content" aria-label="Log content">
        {log?.content ?? (loading ? 'Loading…' : '')}
      </pre>
    </div>
  );
}
