import type { JSX } from 'react';
import { useState } from 'react';
import type { WorkspaceInfo } from '../../../main/host-client';
import { ChatSidebar } from './ChatSidebar';
import { ChatViewHost } from './ChatViewHost';

export type AgentModeProps = {
  // The full workspace list (App owns it); AgentMode filters to rich chats.
  workspaces: WorkspaceInfo[];
  // Shared selection id (reused from App, so a freshly created chat -- which App
  // auto-selects -- shows immediately here).
  selectedId: string | null;
  onSelectChat: (id: string) => void;
  // Creates a rich Copilot chat for the picked repo, then refreshes + selects it.
  onCreateChat: (repoPath: string) => Promise<void>;
};

// AgentMode is the full-screen Copilot Agent shell the app-mode toggle swaps in
// for the standard workspace grid. Two panes: a ChatSidebar (rich-chat list +
// new-chat action) on the left and a ChatViewHost (the selected chat) on the
// right. The conversation transcript, top bar, nav and composer inside the host
// are separate downstream tasks; this builds the shell + host placeholder.
export function AgentMode({
  workspaces,
  selectedId,
  onSelectChat,
  onCreateChat,
}: AgentModeProps): JSX.Element {
  const [creating, setCreating] = useState(false);
  const [error, setError] = useState<string | null>(null);

  // Only rich (Copilot SDK) workspaces are chats; terminal worktrees stay in the
  // standard view.
  const chats = workspaces.filter((w) => w.kind === 'rich');
  const selectedChat = chats.find((w) => w.id === selectedId) ?? null;

  const handleNewChat = async (): Promise<void> => {
    setError(null);
    const repoPath = await window.cs.pickFolder();
    if (!repoPath) return; // user cancelled the folder picker
    setCreating(true);
    try {
      await onCreateChat(repoPath);
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setCreating(false);
    }
  };

  return (
    <main className="app-mode-agent">
      <ChatSidebar
        chats={chats}
        selectedId={selectedId}
        onSelect={onSelectChat}
        onNewChat={() => void handleNewChat()}
        busy={creating}
        error={error}
      />
      <section className="app-mode-agent__main">
        {selectedChat ? (
          <ChatViewHost workspace={selectedChat} />
        ) : (
          <div className="app-mode-agent__empty">
            <p className="app-mode-agent__empty-title">Select a chat or start a new one</p>
          </div>
        )}
      </section>
    </main>
  );
}