import { Fragment, useRef } from 'react';
import { Modal, type ModalHandle } from './Modal';
import { SHORTCUT_GROUPS } from './shortcuts';

type HelpModalProps = {
  onClose: () => void;
};

export function HelpModal({ onClose }: HelpModalProps): JSX.Element {
  const modalRef = useRef<ModalHandle>(null);

  return (
    <Modal
      ref={modalRef}
      title="Keyboard Shortcuts"
      onClose={onClose}
      className="modal--help"
      footer={
        <button type="button" onClick={() => modalRef.current?.close()}>
          Close
        </button>
      }
    >
      <div className="help-grid">
        {SHORTCUT_GROUPS.map((group) => (
          <div className="help-group" key={group.heading}>
            <h3>{group.heading}</h3>
            <dl>
              {group.items.map((item) => (
                <Fragment key={item.keys}>
                  <dt>{item.keys}</dt>
                  <dd>{item.description}</dd>
                </Fragment>
              ))}
            </dl>
          </div>
        ))}
      </div>
    </Modal>
  );
}
