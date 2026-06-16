import type { ReactNode } from 'react';
import type { WorkspaceInfo } from '../../../main/host-client';

type CenterPaneProps = {
  workspace: WorkspaceInfo | null;
  onToggleAutoYes: (enabled: boolean) => void;
  terminal: ReactNode;
  composer: ReactNode;
};

export function CenterPane({ workspace, onToggleAutoYes, terminal, composer }: CenterPaneProps): JSX.Element {
  return (
    <section className="center-pane">
      <div className="tab-bar" role="tablist" aria-label="Workspace views">
        <button className="tab tab--active" type="button">
          Agent
        </button>
        <button className="tab" type="button" disabled title="Coming soon">
          Files
        </button>
        <button className="tab" type="button" disabled title="Coming soon">
          Terminal
        </button>
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
      <div className="agent-surface">{terminal}</div>
      {composer}
    </section>
  );
}
