import type { WorkspaceInfo } from '../../../main/host-client';
import { TermView } from './TermView';
import { CenterTerminal } from './CenterTerminal';

type CenterPaneProps = {
  workspace: WorkspaceInfo | null;
  onToggleAutoYes: (enabled: boolean) => void;
  onRegenerate: () => void;
  regenerating: boolean;
  regenPhase?: string;
  onKillNow: () => void;
};

const regenPhaseCopy: Record<string, string> = {
  handoff: 'Asking the agent to write HANDOFF.md…',
  restarting: 'Restarting the agent…',
  seeding: 'Seeding the new agent with the handoff…',
};

// CenterPane shows the agent terminal (always visible) over a collapsible,
// drag-resizable shell dock (CenterTerminal). Files and Changes live in the
// RightPanel so they're visible at the same time as the agent.
export function CenterPane({
  workspace,
  onToggleAutoYes,
  onRegenerate,
  regenerating,
  regenPhase,
  onKillNow,
}: CenterPaneProps): JSX.Element {
  return (
    <section className="center-pane">
      <div className="tab-bar" aria-label="Agent">
        <span className="pane-title">Agent</span>
        <div className="tab-bar__spacer" />
        {workspace && (
          <>
            <label className="autoyes" title="Auto-approve agent prompts (host-side)">
              <input
                type="checkbox"
                checked={workspace.autoYes}
                onChange={(e) => onToggleAutoYes(e.target.checked)}
              />
              AutoYes
            </label>
            <button
              type="button"
              className="regen-btn"
              disabled={regenerating}
              title="Kill the current agent and start a fresh one"
              onClick={onRegenerate}
            >
              ↻ Regenerate
            </button>
          </>
        )}
      </div>

      <div className="tab-content">
        <div className="tab-pane">
          <div className="agent-surface">
            {regenerating && (
              <div className="regen-banner" role="status" aria-live="polite">
                <span className="regen-banner__spinner" aria-hidden="true" />
                <span className="regen-banner__text">
                  {regenPhaseCopy[regenPhase ?? ''] ?? 'Regenerating the agent…'}
                </span>
                {regenPhase === 'handoff' && (
                  <button type="button" className="regen-banner__kill" onClick={onKillNow}>
                    Kill now
                  </button>
                )}
              </div>
            )}
            <TermView
              key={workspace?.sessionName ?? 'none'}
              sessionName={workspace?.sessionName ?? null}
            />
          </div>
        </div>
      </div>

      <CenterTerminal workspace={workspace} />
    </section>
  );
}
