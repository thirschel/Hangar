import type { JSX } from 'react';
import type { WorkspaceInfo } from '../../../main/host-client';
import { ChatViewHost } from './ChatViewHost';
import { CenterTerminal } from './CenterTerminal';

export type AgentModeProps = {
  // The selected rich chat, or null when nothing rich is selected. App owns the
  // chat list (the shared Sidebar) and selection; AgentMode is just the main
  // pane that hosts the selected conversation (or an empty state).
  selectedChat: WorkspaceInfo | null;
};

// AgentMode is the right-hand pane of the Copilot Agent surface the app-mode
// toggle swaps in. The chat list now lives in the shared Sidebar (owned by App),
// so this renders only the selected chat's ChatViewHost or an empty state. Below
// the chat sits the collapsible shell dock (CenterTerminal) so the worktree shell
// is one click away without leaving the conversation.
export function AgentMode({ selectedChat }: AgentModeProps): JSX.Element {
  return (
    <section className="app-mode-agent__main">
      {selectedChat ? (
        <>
          <ChatViewHost workspace={selectedChat} findHotkeyScope="global" />
          <CenterTerminal workspace={selectedChat} />
        </>
      ) : (
        <div className="app-mode-agent__empty">
          <p className="app-mode-agent__empty-title">Select a chat or start a new one</p>
        </div>
      )}
    </section>
  );
}