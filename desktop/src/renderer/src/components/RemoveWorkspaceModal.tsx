import { useCallback, useState, useRef, type FormEvent } from 'react';
import { Modal, type ModalHandle } from './Modal';

type RemoveWorkspaceModalProps = {
  workspaceTitle: string;
  hasUncommittedChanges: boolean;
  onConfirm: (deleteWorktree: boolean) => Promise<void>;
  onClose: () => void;
};

/**
 * Confirmation modal for removing a workspace from the manager.
 * 
 * By default, removing a workspace KEEPS the worktree directory and branch on disk
 * (so you can continue working from the CLI or re-open it later). The user can opt-in
 * to also delete the worktree directory and its branch via a checkbox.
 * 
 * Shows a warning when the worktree has uncommitted changes.
 */
export function RemoveWorkspaceModal({
  workspaceTitle,
  hasUncommittedChanges,
  onConfirm,
  onClose,
}: RemoveWorkspaceModalProps): JSX.Element {
  const [deleteWorktree, setDeleteWorktree] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const modalRef = useRef<ModalHandle>(null);

  const onSubmit = useCallback(
    async (e: FormEvent): Promise<void> => {
      e.preventDefault();
      setBusy(true);
      setError(null);
      try {
        await onConfirm(deleteWorktree);
        modalRef.current?.close();
      } catch (err: unknown) {
        setBusy(false);
        setError(err instanceof Error ? err.message : String(err));
      }
    },
    [onConfirm, deleteWorktree],
  );

  return (
    <Modal
      ref={modalRef}
      title="Remove workspace?"
      onClose={onClose}
      error={error}
      busy={busy}
      className="modal--remove-workspace"
      footer={
        <>
          <button type="button" onClick={onClose} disabled={busy}>
            Cancel
          </button>
          <button
            type="submit"
            form="remove-workspace-form"
            className="button--primary"
            disabled={busy}
          >
            {busy ? 'Removing…' : 'Remove workspace'}
          </button>
        </>
      }
    >
      <form id="remove-workspace-form" onSubmit={onSubmit}>
        <p>
          <strong>"{workspaceTitle}"</strong> will be removed from Hangar, and the agent session
          will be stopped.
        </p>
        
        {hasUncommittedChanges && (
          <p className="warning">
            ⚠️ This workspace has uncommitted changes.
          </p>
        )}

        <label className="checkbox-label">
          <input
            type="checkbox"
            checked={deleteWorktree}
            onChange={(e) => setDeleteWorktree(e.target.checked)}
            disabled={busy}
          />
          <span>
            Also delete the worktree directory and its branch from disk
          </span>
        </label>

        <p className="help-text">
          {deleteWorktree ? (
            <>
              The worktree directory and branch will be <strong>permanently deleted</strong>.
              Any uncommitted work will be lost.
            </>
          ) : (
            <>
              The worktree directory and branch will be kept on disk.
              You can continue working from the CLI or re-open it later.
            </>
          )}
        </p>
      </form>
    </Modal>
  );
}
