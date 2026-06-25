import type { JSX } from 'react';
import type { WorkspaceInfo } from '../../../main/host-client';
import { relativeTime } from '../lib/time';

export type ChatSidebarProps = {
  // Already filtered to rich chats by the parent (AgentMode).
  chats: WorkspaceInfo[];
  selectedId: string | null;
  onSelect: (id: string) => void;
  onNewChat: () => void;
  // True while a new chat is being created (folder pick + CreateWorkspace).
  busy?: boolean;
  // Surfaced when the last new-chat attempt failed.
  error?: string | null;
};

// Show just the last path segment as the repo name
// (e.g. "C:\src\Hangar" -> "Hangar").
function repoName(repoPath: string): string {
  const segments = repoPath.split(/[\\/]/).filter(Boolean);
  return segments.length > 0 ? segments[segments.length - 1] : repoPath;
}

// ChatSidebar is the left pane of the Agent shell: a prominent "New chat" action
// plus the list of rich Copilot chats. Intentionally minimal -- no archive,
// settings, grid or status clutter -- so the list reads like a messaging app.
export function ChatSidebar({
  chats,
  selectedId,
  onSelect,
  onNewChat,
  busy = false,
  error,
}: ChatSidebarProps): JSX.Element {
  return (
    <aside className="chat-sidebar">
      <div className="chat-sidebar__header">
        <button type="button" className="chat-sidebar__new" onClick={onNewChat} disabled={busy}>
          {busy ? 'Starting…' : '+ New chat'}
        </button>
      </div>
      {error && (
        <div className="chat-sidebar__error" role="alert">
          {error}
        </div>
      )}
      <nav className="chat-sidebar__list" aria-label="Chats">
        {chats.length === 0 ? (
          <div className="chat-sidebar__empty">No chats yet. Start a new one.</div>
        ) : (
          chats.map((chat) => (
            <button
              key={chat.id}
              type="button"
              className={`chat-row${chat.id === selectedId ? ' chat-row--selected' : ''}`}
              onClick={() => onSelect(chat.id)}
            >
              <span className="chat-row__title">{chat.title}</span>
              <span className="chat-row__meta">
                <span className="chat-row__repo">{repoName(chat.repoPath)}</span>
                <span className="chat-row__branch">{chat.branch}</span>
                {chat.lastOutputUnix > 0 && (
                  <span className="chat-row__time">{relativeTime(chat.lastOutputUnix)}</span>
                )}
              </span>
            </button>
          ))
        )}
      </nav>
    </aside>
  );
}