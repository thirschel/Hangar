import type { JSX } from 'react';
import {
  forwardRef,
  useCallback,
  useEffect,
  useImperativeHandle,
  useRef,
  useState,
  type ReactNode,
} from 'react';

export type ModalHandle = {
  /** Play the exit animation, then unmount via onClose. Ignores the busy guard. */
  close: () => void;
};

type ModalProps = {
  title: ReactNode;
  /** Called once the exit animation has finished — the parent should unmount the modal. */
  onClose: () => void;
  children: ReactNode;
  footer?: ReactNode;
  error?: string | null;
  /** Extra class on the .modal element (e.g. "modal--create"). */
  className?: string;
  /** While true, Esc / backdrop clicks won't dismiss (e.g. a request is in flight). */
  busy?: boolean;
};

/**
 * Shared modal shell: dimmed + blurred backdrop, scale/fade enter & exit animations,
 * Esc / click-outside dismissal, and focus containment. Used by the Settings and
 * Create-workspace modals. Programmatic closes (after a successful save/create) go
 * through the imperative `close()` handle so the exit animation still plays.
 */
export const Modal = forwardRef<ModalHandle, ModalProps>(function Modal(
  { title, onClose, children, footer, error, className, busy },
  ref,
): JSX.Element {
  const [closing, setClosing] = useState(false);
  const overlayRef = useRef<HTMLDivElement>(null);
  const busyRef = useRef(busy);
  busyRef.current = busy;

  const close = useCallback((force: boolean): void => {
    if (!force && busyRef.current) return;
    setClosing(true);
  }, []);

  useImperativeHandle(ref, () => ({ close: () => close(true) }), [close]);

  // Modal owns Esc so the exit animation plays (capture phase, so it wins over
  // any app-level Escape handlers).
  useEffect(() => {
    const onKey = (e: KeyboardEvent): void => {
      if (e.key === 'Escape') {
        e.stopPropagation();
        close(false);
      }
    };
    window.addEventListener('keydown', onKey, true);
    return () => window.removeEventListener('keydown', onKey, true);
  }, [close]);

  const onOverlayAnimEnd = (e: React.AnimationEvent<HTMLDivElement>): void => {
    // Only the overlay's own (exit) animation should unmount — ignore bubbling
    // animations from the inner modal.
    if (closing && e.target === overlayRef.current) onClose();
  };

  return (
    <div
      ref={overlayRef}
      className={`modal-overlay${closing ? ' modal-overlay--closing' : ''}`}
      onClick={() => close(false)}
      onAnimationEnd={onOverlayAnimEnd}
    >
      <div
        className={`modal${className ? ` ${className}` : ''}`}
        role="dialog"
        aria-modal="true"
        onClick={(e) => e.stopPropagation()}
      >
        <div className="modal__header">{title}</div>
        <div className="modal__body">{children}</div>
        {error && <div className="modal__error">{error}</div>}
        {footer && <div className="modal__footer">{footer}</div>}
      </div>
    </div>
  );
});
