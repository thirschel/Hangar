import type { JSX } from 'react';
import { useRef, useState } from 'react';
import { Modal, type ModalHandle } from './Modal';

type WelcomeModalProps = {
  onClose: () => void;
};

export function WelcomeModal({ onClose }: WelcomeModalProps): JSX.Element {
  const [busy, setBusy] = useState(false);
  const modalRef = useRef<ModalHandle>(null);

  const handleChoice = async (autoUpdate: boolean): Promise<void> => {
    setBusy(true);
    try {
      await window.cs.completeSetup?.({ autoUpdate });
    } catch {
      // Non-fatal — settings are best-effort.
    }
    modalRef.current?.close();
  };

  return (
    <Modal
      ref={modalRef}
      title="Welcome to Hangar"
      onClose={onClose}
      busy={busy}
      className="modal--welcome"
      footer={
        <>
          <button
            type="button"
            onClick={() => void handleChoice(false)}
            disabled={busy}
          >
            No thanks
          </button>
          <button
            type="button"
            className="modal__primary"
            onClick={() => void handleChoice(true)}
            disabled={busy}
          >
            Enable auto-updates
          </button>
        </>
      }
    >
      <p>Would you like to receive automatic updates?</p>
      <p className="welcome-detail">
        When enabled, the app will check for new versions from GitHub and download
        them in the background. You&apos;ll always be prompted before installing.
      </p>
      <p className="welcome-detail">
        You can change this anytime in Settings.
      </p>
    </Modal>
  );
}
