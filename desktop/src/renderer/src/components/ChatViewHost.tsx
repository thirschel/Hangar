import type { JSX } from 'react';
import type { WorkspaceInfo } from '../../../main/host-client';

// ChatViewHost is the right-pane host for a single rich (Copilot) chat. This is
// the SEAM for the downstream agent-shell work: a later task (ui-a-chatview /
// ui-a-composer) replaces the body below with the real top bar, message nav and
// conversation transcript + composer. For now it renders a minimal placeholder
// so the 2-pane Agent shell has a working surface once a chat is selected. The
// prop contract is stable -- downstream keeps `{ workspace: WorkspaceInfo }` and
// fills in the body.
export type ChatViewHostProps = {
  workspace: WorkspaceInfo;
};

export function ChatViewHost({ workspace }: ChatViewHostProps): JSX.Element {
  return (
    <section className="chat-view" aria-label="Chat conversation">
      <header className="chat-view__header">
        <h2 className="chat-view__title">{workspace.title}</h2>
        <span className="chat-view__branch">{workspace.branch}</span>
      </header>
      <div className="chat-view__placeholder">
        <p className="chat-view__placeholder-text">Conversation coming soon</p>
      </div>
    </section>
  );
}