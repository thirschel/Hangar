import type { JSX } from 'react';

// AgentMode is a placeholder for the full-screen Copilot Agent experience that
// the app-mode toggle swaps in for the standard workspace grid. It renders a
// minimal two-pane scaffold - a left "Chats" sidebar column and a right main
// column - so the toggle has a real surface to flip to. A downstream task
// replaces this stub with the live agent shell (chat list + conversation view).
// The root `app-mode-agent` class namespaces all agent-mode styling.
export function AgentMode(): JSX.Element {
  return (
    <main className="app-mode-agent">
      <aside className="app-mode-agent__sidebar">
        <h2 className="app-mode-agent__sidebar-title">Chats</h2>
      </aside>
      <section className="app-mode-agent__main">
        <p className="app-mode-agent__placeholder">Agent mode — chat surface coming soon</p>
      </section>
    </main>
  );
}