import { useEffect, useRef, useState } from 'react';
import { Modal, type ModalHandle } from './Modal';
import type { Settings } from '../../../main/settings';

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

export function SettingsModal({ onClose, onSaved }: SettingsModalProps): JSX.Element {
  const [settings, setSettings] = useState<Settings | null>(null);
  const [busy, setBusy] = useState(false);
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

  return (
    <Modal
      ref={modalRef}
      title="Settings"
      onClose={onClose}
      busy={busy}
      error={error}
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
        </>
      )}
    </Modal>
  );
}
