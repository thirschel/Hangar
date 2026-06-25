import type { JSX } from 'react';
import type { McpServerInfo } from '../../../main/host-client';

// MCP servers page (full-middle, read-only). Renders the latest `mcp.detail`
// snapshot accumulated by ChatViewHost: one card per server with a status badge,
// transport, source, an optional error and its advertised tools. The status
// label/colour vocabulary intentionally mirrors the inline MCP pill bar in
// ChatViewHost; it is duplicated here (rather than imported) to avoid a
// component import cycle, and both map to the same :root colour tokens.
const MCP_STATUS_META: Record<string, { label: string; className: string }> = {
  connected: { label: 'Connected', className: 'ok' },
  failed: { label: 'Failed', className: 'error' },
  'needs-auth': { label: 'Needs auth', className: 'warn' },
  pending: { label: 'Pending', className: 'warn' },
  disabled: { label: 'Disabled', className: 'muted' },
  not_configured: { label: 'Not configured', className: 'muted' },
};

function statusMeta(status?: string): { label: string; className: string } {
  if (!status) return { label: 'Unknown', className: 'muted' };
  return MCP_STATUS_META[status] ?? { label: status, className: 'muted' };
}

export function McpPage({ servers }: { servers: McpServerInfo[] }): JSX.Element {
  if (servers.length === 0) {
    return (
      <div className="mcp-page mcp-page--empty">
        <p className="mcp-page__empty-text">No MCP servers configured.</p>
      </div>
    );
  }

  return (
    <div className="mcp-page" aria-label="MCP servers">
      {servers.map((server) => {
        const meta = statusMeta(server.status);
        return (
          <article key={server.name} className="mcp-page__card">
            <header className="mcp-page__head">
              <span className="mcp-page__name">{server.name}</span>
              <span className={`mcp-page__status mcp-page__status--${meta.className}`}>
                {meta.label}
              </span>
            </header>

            <dl className="mcp-page__meta">
              {server.transport && (
                <div className="mcp-page__meta-row">
                  <dt className="mcp-page__meta-key">Transport</dt>
                  <dd className="mcp-page__meta-val">{server.transport}</dd>
                </div>
              )}
              {server.source && (
                <div className="mcp-page__meta-row">
                  <dt className="mcp-page__meta-key">Source</dt>
                  <dd className="mcp-page__meta-val">{server.source}</dd>
                </div>
              )}
            </dl>

            {server.error && (
              <p className="mcp-page__error" role="alert">
                {server.error}
              </p>
            )}

            {server.tools && server.tools.length > 0 && (
              <div className="mcp-page__tools">
                <span className="mcp-page__tools-label">Tools</span>
                <ul className="mcp-page__tool-list">
                  {server.tools.map((tool) => (
                    <li key={tool} className="mcp-page__tool">
                      {tool}
                    </li>
                  ))}
                </ul>
              </div>
            )}
          </article>
        );
      })}
    </div>
  );
}
