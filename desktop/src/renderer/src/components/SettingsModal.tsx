import { useEffect, useRef, useState } from 'react';
import { Modal, type ModalHandle } from './Modal';
import type { Settings } from '../../../main/settings';

type SettingsModalProps = {
  onClose: () => void;
  onSaved?: (next: Settings) => void;
};

export function SettingsModal({ onClose, onSaved }: SettingsModalProps): JSX.Element {
  const [settings, setSettings] = useState<Settings | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
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
                placeholder="~/.claude-squad/worktrees (default)"
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
        </>
      )}
    </Modal>
  );
}
