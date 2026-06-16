import { useEffect, useState } from 'react';
import type { Settings } from '../../../main/settings';

type SettingsModalProps = {
  onClose: () => void;
  onSaved?: (next: Settings) => void;
};

export function SettingsModal({ onClose, onSaved }: SettingsModalProps): JSX.Element {
  const [settings, setSettings] = useState<Settings | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

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

  const save = async (): Promise<void> => {
    if (!settings) return;
    setBusy(true);
    setError(null);
    try {
      const next = await window.cs.setSettings(settings);
      onSaved?.(next);
      onClose();
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="modal-overlay" onClick={onClose}>
      <div className="modal" onClick={(e) => e.stopPropagation()}>
        <div className="modal__header">Settings</div>
        {!settings ? (
          <div className="modal__body">Loading…</div>
        ) : (
          <div className="modal__body">
            <label className="field">
              <span>Default agent</span>
              <input
                value={settings.defaultProgram}
                spellCheck={false}
                onChange={(e) => patch({ defaultProgram: e.target.value })}
              />
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
          </div>
        )}
        {error && <div className="modal__error">{error}</div>}
        <div className="modal__footer">
          <button type="button" onClick={onClose} disabled={busy}>
            Cancel
          </button>
          <button type="button" className="modal__primary" onClick={() => void save()} disabled={busy || !settings}>
            Save
          </button>
        </div>
      </div>
    </div>
  );
}
