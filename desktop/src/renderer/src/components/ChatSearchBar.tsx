import type { JSX } from 'react';
import { useEffect, useRef } from 'react';

export type ChatSearchBarProps = {
  query: string;
  onQueryChange: (q: string) => void;
  matchCount: number;
  // 1-based position of the active match (0 when there are none).
  activeOrdinal: number;
  onNext: () => void;
  onPrev: () => void;
  onClose: () => void;
};

// A compact find bar pinned to the top-right of the chat transcript. Enter jumps
// to the next match, Shift+Enter the previous, and Escape closes. It is a thin
// controlled view over useTranscriptSearch (which owns matching + highlighting).
export function ChatSearchBar({
  query,
  onQueryChange,
  matchCount,
  activeOrdinal,
  onNext,
  onPrev,
  onClose,
}: ChatSearchBarProps): JSX.Element {
  const inputRef = useRef<HTMLInputElement>(null);

  // Focus + select on open so the user can type (or replace) immediately.
  useEffect(() => {
    inputRef.current?.focus();
    inputRef.current?.select();
  }, []);

  const noMatches = matchCount === 0;

  return (
    <div className="chat-search-bar" role="search">
      <input
        ref={inputRef}
        className="chat-search-bar__input"
        type="text"
        placeholder="Find in chat…"
        aria-label="Find in chat"
        value={query}
        onChange={(e) => onQueryChange(e.target.value)}
        onKeyDown={(e) => {
          if (e.key === 'Enter') {
            e.preventDefault();
            if (e.shiftKey) onPrev();
            else onNext();
          } else if (e.key === 'Escape') {
            e.preventDefault();
            onClose();
          }
        }}
      />
      <span className="chat-search-bar__count" aria-live="polite">
        {query ? `${activeOrdinal}/${matchCount}` : ''}
      </span>
      <button
        type="button"
        className="chat-search-bar__btn"
        title="Previous match (Shift+Enter)"
        aria-label="Previous match"
        disabled={noMatches}
        onClick={onPrev}
      >
        ↑
      </button>
      <button
        type="button"
        className="chat-search-bar__btn"
        title="Next match (Enter)"
        aria-label="Next match"
        disabled={noMatches}
        onClick={onNext}
      >
        ↓
      </button>
      <button
        type="button"
        className="chat-search-bar__btn chat-search-bar__close"
        title="Close (Esc)"
        aria-label="Close search"
        onClick={onClose}
      >
        ✕
      </button>
    </div>
  );
}
