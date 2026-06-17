import { useEffect, useRef, useState } from 'react';
import { Modal, type ModalHandle } from './Modal';
import type { CopilotSessionInfo } from '../../../main/host-client';

type SessionBrowserModalProps = {
  onClose: () => void;
  onResume: (session: CopilotSessionInfo) => Promise<void>;
};

function relativeTime(unix: number): string {
  const diff = Math.max(0, Math.floor(Date.now() / 1000 - unix));
  if (diff < 60) return 'just now';
  if (diff < 3600) return `${Math.floor(diff / 60)}m ago`;
  if (diff < 86400) return `${Math.floor(diff / 3600)}h ago`;
  return `${Math.floor(diff / 86400)}d ago`;
}

export function SessionBrowserModal({
  onClose,
  onResume,
}: SessionBrowserModalProps): JSX.Element {
  const [sessions, setSessions] = useState<CopilotSessionInfo[]>([]);
  const [filter, setFilter] = useState('');
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [resuming, setResuming] = useState<string | null>(null);
  const [skipped, setSkipped] = useState(0);
  const modalRef = useRef<ModalHandle>(null);
  const filterRef = useRef<HTMLInputElement>(null);

  useEffect(() => {
    let active = true;
    void (async () => {
      try {
        const result = await window.cs.listCopilotSessions();
        if (!active) return;
        setSessions(result.sessions);
        setSkipped(result.skipped);
      } catch (e) {
        if (active) setError(e instanceof Error ? e.message : String(e));
      } finally {
        if (active) setLoading(false);
      }
    })();
    return () => {
      active = false;
    };
  }, []);

  useEffect(() => {
    if (!loading && filterRef.current) filterRef.current.focus();
  }, [loading]);

  const filtered = filter
    ? sessions.filter((s) => {
        const q = filter.toLowerCase();
        return (
          s.name.toLowerCase().includes(q) ||
          s.repository.toLowerCase().includes(q) ||
          s.branch.toLowerCase().includes(q)
        );
      })
    : sessions;

  const handleResume = async (s: CopilotSessionInfo): Promise<void> => {
    if (s.inUse || resuming) return;
    setResuming(s.id);
    setError(null);
    try {
      await onResume(s);
      modalRef.current?.close();
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setResuming(null);
    }
  };

  return (
    <Modal
      ref={modalRef}
      className="modal--browser"
      title="Copilot Session Browser"
      onClose={onClose}
      busy={!!resuming}
      error={error}
      footer={
        <button type="button" onClick={() => modalRef.current?.close()}>
          Close (Esc)
        </button>
      }
    >
      {loading ? (
        <div className="browser-loading">Discovering sessions…</div>
      ) : (
        <>
          <div className="browser-search">
            <input
              ref={filterRef}
              className="browser-search__input"
              type="text"
              placeholder="Filter sessions…"
              value={filter}
              onChange={(e) => setFilter(e.target.value)}
              data-is-input="true"
            />
            <span className="browser-search__count">
              {filtered.length} session{filtered.length === 1 ? '' : 's'}
              {skipped > 0 && ` · ${skipped} skipped`}
            </span>
          </div>
          <div className="browser-list">
            {filtered.length === 0 && (
              <div className="browser-empty">
                {sessions.length === 0 ? 'No Copilot sessions found' : 'No matches'}
              </div>
            )}
            {filtered.map((s) => (
              <div
                key={s.id}
                className={`browser-item${s.inUse ? ' browser-item--in-use' : ''}${resuming === s.id ? ' browser-item--resuming' : ''}`}
                onClick={() => void handleResume(s)}
                role="button"
                tabIndex={0}
              >
                <div className="browser-item__header">
                  <span className="browser-item__name">{s.name}</span>
                  <span className="browser-item__time">{relativeTime(s.updatedAt)}</span>
                </div>
                <div className="browser-item__meta">
                  {s.repository && (
                    <span className="browser-item__repo">{s.repository}</span>
                  )}
                  {s.branch && (
                    <span className="browser-item__branch">{s.branch}</span>
                  )}
                  {s.inUse && <span className="browser-item__badge">in use</span>}
                </div>
              </div>
            ))}
          </div>
        </>
      )}
    </Modal>
  );
}
