import type { JSX } from 'react';
import { useRef, useState } from 'react';
import { Modal, type ModalHandle } from './Modal';
import type { WorkspaceInfo } from '../../../main/host-client';

type WorkspaceSettingsModalProps = {
  workspace: WorkspaceInfo;
  onClose: () => void;
  onSaved: () => void;
};

export function WorkspaceSettingsModal({
  workspace,
  onClose,
  onSaved,
}: WorkspaceSettingsModalProps): JSX.Element {
  const [title, setTitle] = useState(workspace.title);
  const [program, setProgram] = useState(workspace.program);
  const [shell, setShell] = useState(workspace.shell || 'cmd');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const modalRef = useRef<ModalHandle>(null);

  const save = async (): Promise<void> => {
    setBusy(true);
    setError(null);
    try {
      const patch: { title?: string; program?: string; shell?: string } = {};
      if (title.trim() && title !== workspace.title) patch.title = title.trim();
      if (program.trim() && program !== workspace.program) patch.program = program.trim();
      if (shell !== (workspace.shell || 'cmd')) patch.shell = shell;
      await window.cs.updateWorkspace(workspace.id, patch);
      onSaved();
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
      title="Workspace Settings"
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
            disabled={busy}
          >
            {busy ? 'Saving…' : 'Save'}
          </button>
        </>
      }
    >
      <div className="create-form">
        <label>
          Repository
          <input value={workspace.repoPath} disabled />
        </label>
        <label>
          Branch
          <input value={workspace.branch} disabled />
        </label>
        <label>
          Title
          <input
            autoFocus
            value={title}
            onChange={(e) => setTitle(e.target.value)}
          />
        </label>
        <label>
          Agent
          <input
            value={program}
            onChange={(e) => setProgram(e.target.value)}
          />
        </label>
        <label>
          Shell
          <span className="hint">(takes effect on next regenerate)</span>
          <select value={shell} onChange={(e) => setShell(e.target.value)}>
            <option value="cmd">cmd.exe</option>
            <option value="powershell">PowerShell 5 (powershell.exe)</option>
            <option value="pwsh">PowerShell 7 (pwsh.exe)</option>
          </select>
        </label>
      </div>
    </Modal>
  );
}
