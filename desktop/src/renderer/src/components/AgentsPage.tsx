import type { JSX } from 'react';
import type { AgentInfo } from '../../../main/host-client';

// Agents page (full-middle, read-only). Renders the latest `agents` snapshot
// discovered for the session: one card per custom agent with its name, source, a
// subagent-only marker, description, preferred model, preloaded skills/tools,
// attached MCP server names, and file path. Purely informational -- there is no
// selection or actions here.
export function AgentsPage({ agents }: { agents: AgentInfo[] }): JSX.Element {
  if (agents.length === 0) {
    return (
      <div className="agents-page agents-page--empty">
        <p className="agents-page__empty-text">No custom agents discovered.</p>
      </div>
    );
  }

  return (
    <div className="agents-page" aria-label="Agents">
      {agents.map((agent) => {
        const title = agent.displayName || agent.name;
        return (
          <article key={agent.name} className="agents-page__card">
            <header className="agents-page__head">
              <span className="agents-page__name">{title}</span>
              <span className="agents-page__badges">
                {agent.userInvocable === false && (
                  <span className="agents-page__badge agents-page__badge--subagent">
                    Subagent
                  </span>
                )}
                {agent.source && <span className="agents-page__badge">{agent.source}</span>}
              </span>
            </header>

            {agent.displayName && agent.displayName !== agent.name && (
              <span className="agents-page__id">{agent.name}</span>
            )}

            {agent.description && <p className="agents-page__description">{agent.description}</p>}

            <dl className="agents-page__meta">
              {agent.model && (
                <div className="agents-page__meta-row">
                  <dt className="agents-page__meta-key">Model</dt>
                  <dd className="agents-page__meta-val">{agent.model}</dd>
                </div>
              )}
              {agent.skills && agent.skills.length > 0 && (
                <div className="agents-page__meta-row">
                  <dt className="agents-page__meta-key">Skills</dt>
                  <dd className="agents-page__meta-val">{agent.skills.join(', ')}</dd>
                </div>
              )}
              {agent.tools && agent.tools.length > 0 && (
                <div className="agents-page__meta-row">
                  <dt className="agents-page__meta-key">Tools</dt>
                  <dd className="agents-page__meta-val">{agent.tools.join(', ')}</dd>
                </div>
              )}
              {agent.mcpServerNames && agent.mcpServerNames.length > 0 && (
                <div className="agents-page__meta-row">
                  <dt className="agents-page__meta-key">MCP</dt>
                  <dd className="agents-page__meta-val">{agent.mcpServerNames.join(', ')}</dd>
                </div>
              )}
              {agent.path && (
                <div className="agents-page__meta-row">
                  <dt className="agents-page__meta-key">Path</dt>
                  <dd className="agents-page__meta-val agents-page__path">{agent.path}</dd>
                </div>
              )}
            </dl>
          </article>
        );
      })}
    </div>
  );
}
