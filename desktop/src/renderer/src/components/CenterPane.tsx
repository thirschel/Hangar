import type { JSX } from 'react';
import type { WorkspaceInfo } from '../../../main/host-client';
import { TermView } from './TermView';
import { CenterTerminal } from './CenterTerminal';
import { TranscriptView } from './TranscriptView';

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

// CenterPane shows either the terminal agent view or the rich transcript view.
// Terminal workspaces also keep the collapsible shell dock below the agent.
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
            {workspace?.kind === 'rich' ? (
              <TranscriptView key={workspace.sessionName} sessionName={workspace.sessionName} />
            ) : (
              <TermView
                key={workspace?.sessionName ?? 'none'}
                sessionName={workspace?.sessionName ?? null}
              />
            )}
          </div>
        </div>
      </div>

      {workspace?.kind !== 'rich' && <CenterTerminal workspace={workspace} />}
    </section>
  );
}
