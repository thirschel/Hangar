import { useRef, useState } from 'react';
import type { WorkspaceInfo } from '../../../main/host-client';
import { Modal, type ModalHandle } from './Modal';

type RegenerateModalProps = {
  workspace: WorkspaceInfo;
  onConfirm: (handoff: boolean) => void;
  onClose: () => void;
};

// RegenerateModal is a compact confirm-only popup. Once confirmed it closes
// immediately; in-progress state is shown by a non-blocking banner over the agent
// terminal (see CenterPane) so the user can still watch the agent and isn't
// screen-blocked.
export function RegenerateModal({
  workspace,
  onConfirm,
  onClose,
}: RegenerateModalProps): JSX.Element {
  const [handoff, setHandoff] = useState(true);
  const modalRef = useRef<ModalHandle>(null);

  const confirm = (): void => {
    onConfirm(handoff);
    modalRef.current?.close();
  };

  return (
    <Modal
      ref={modalRef}
      title="Regenerate agent?"
      onClose={onClose}
      footer={
        <>
          <button type="button" onClick={() => modalRef.current?.close()}>
            Cancel
          </button>
          <button type="button" className="modal__primary" onClick={confirm}>
            Regenerate
          </button>
        </>
      }
    >
      <p className="regen-copy">
        This kills the current agent for <strong>{workspace.title}</strong> and starts a fresh one in
        the same worktree and branch. Your files and changes are kept.
      </p>
      <label className="field field--row regen-handoff">
        <input type="checkbox" checked={handoff} onChange={(e) => setHandoff(e.target.checked)} />
        <span>Create a handoff document (HANDOFF.md) and seed the new agent with it.</span>
      </label>
      <div className="regen-helper">
        Preserves context. Takes longer and uses tokens. Overwrites any existing HANDOFF.md.
      </div>
    </Modal>
  );
}
