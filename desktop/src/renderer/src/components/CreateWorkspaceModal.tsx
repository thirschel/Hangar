import type { JSX } from 'react';
import { useEffect, useRef, useState } from 'react';
import { Modal, type ModalHandle } from './Modal';
import type { CreateWorkspaceArgs } from '../../../preload';

type CreateWorkspaceModalProps = {
  onClose: () => void;
  onCreate: (args: CreateWorkspaceArgs) => Promise<void>;
  initialRepoPath?: string;
};

export function CreateWorkspaceModal({
  onClose,
  onCreate,
  initialRepoPath,
}: CreateWorkspaceModalProps): JSX.Element {
  const [repoPath, setRepoPath] = useState(initialRepoPath ?? '');
  const [title, setTitle] = useState('');
  const [program, setProgram] = useState('');
  const [defaultProgram, setDefaultProgram] = useState('copilot');
  const [shell, setShell] = useState('');
  const [baseBranch, setBaseBranch] = useState('');
  const [worktree, setWorktree] = useState(true);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const modalRef = useRef<ModalHandle>(null);

  // Pre-fill the agent with the daemon's default so the field is never silently
  // blank (a blank agent falls back to the config default, which can be stale).
  useEffect(() => {
    let active = true;
    window.cs
      .getDefaultProgram()
      .then((prog) => {
        if (!active) return;
        setDefaultProgram(prog);
        setProgram((cur) => cur || prog);
      })
      .catch(() => {
        /* keep the built-in 'copilot' default */
      });
    return () => {
      active = false;
    };
  }, []);

  const browse = async (): Promise<void> => {
    const dir = await window.cs.pickFolder();
    if (dir) setRepoPath(dir);
  };

  const submit = async (): Promise<void> => {
    if (!repoPath.trim()) {
      setError('Repository path is required.');
      return;
    }
    setBusy(true);
    setError(null);
    try {
      await onCreate({
        repoPath: repoPath.trim(),
        title: title.trim() || undefined,
        program: program.trim() || undefined,
        baseBranch: worktree ? baseBranch.trim() || undefined : undefined,
        shell: shell || undefined,
        noWorktree: worktree ? undefined : true,
      });
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
      className="modal--create"
      title="New workspace"
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
            onClick={() => void submit()}
            disabled={busy}
          >
            {busy ? 'Creating…' : 'Create workspace'}
          </button>
        </>
      }
    >
      <div className="create-form">
        <label>
          Repository
          <div className="row">
            <input
              autoFocus
              value={repoPath}
              onChange={(e) => setRepoPath(e.target.value)}
              placeholder="C:\path\to\repo"
            />
            <button type="button" onClick={browse}>
              Browse…
            </button>
          </div>
        </label>
        <label>
          Title{' '}
          <span className="hint">(optional — the agent names it after your first message)</span>
          <input
            value={title}
            onChange={(e) => setTitle(e.target.value)}
            placeholder="Auto-named from your first message"
          />
        </label>
        <label>
          Agent <span className="hint">(must be on PATH or a PowerShell function)</span>
          <input
            value={program}
            onChange={(e) => setProgram(e.target.value)}
            placeholder={defaultProgram}
          />
        </label>
        <label>
          Shell <span className="hint">(defaults to Settings value)</span>
          <select value={shell} onChange={(e) => setShell(e.target.value)}>
            <option value="">Default (from Settings)</option>
            <option value="cmd">cmd.exe</option>
            <option value="powershell">PowerShell 5 (powershell.exe)</option>
            <option value="pwsh">PowerShell 7 (pwsh.exe)</option>
          </select>
        </label>
        <label className="create-form__check">
          <input
            type="checkbox"
            checked={worktree}
            onChange={(e) => setWorktree(e.target.checked)}
          />
          Create an isolated git worktree{' '}
          <span className="hint">(off: open the agent directly in the selected folder)</span>
        </label>
        {worktree && (
          <label>
            Base branch <span className="hint">(optional)</span>
            <input
              value={baseBranch}
              onChange={(e) => setBaseBranch(e.target.value)}
              placeholder="main"
            />
          </label>
        )}
      </div>
    </Modal>
  );
}
