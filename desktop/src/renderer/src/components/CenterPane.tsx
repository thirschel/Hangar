import type { ReactNode } from 'react';
import type { WorkspaceInfo } from '../../../main/host-client';
import { TermView } from './TermView';

type CenterPaneProps = {
  workspace: WorkspaceInfo | null;
  onToggleAutoYes: (enabled: boolean) => void;
  composer: ReactNode;
};

// CenterPane shows the agent terminal (always visible) plus the composer. Files,
// Terminal, Changes and Run live in the RightPanel so they're visible at the same
// time as the agent.
export function CenterPane({ workspace, onToggleAutoYes, composer }: CenterPaneProps): JSX.Element {
  return (
    <section className="center-pane">
      <div className="tab-bar" aria-label="Agent">
        <span className="pane-title">Agent</span>
        <div className="tab-bar__spacer" />
        {workspace && (
          <label className="autoyes" title="Auto-approve agent prompts (host-side)">
            <input
              type="checkbox"
              checked={workspace.autoYes}
              onChange={(e) => onToggleAutoYes(e.target.checked)}
            />
            AutoYes
          </label>
        )}
      </div>

      <div className="tab-content">
        <div className="tab-pane">
          <div className="agent-surface">
            <TermView
              key={workspace?.sessionName ?? 'none'}
              sessionName={workspace?.sessionName ?? null}
            />
          </div>
          {composer}
        </div>
      </div>
    </section>
  );
}
