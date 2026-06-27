import type { FormEvent, JSX } from 'react';
import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import type { McpServerInfo, WorkspaceInfo } from '../../../main/host-client';
import type { McpCatalog, McpServerDef } from '../../../preload';
import { Modal, type ModalHandle } from './Modal';
import { RegenerateModal } from './RegenerateModal';

// MCP servers page. It combines the daemon-reported live status snapshot with
// the editable global catalog and per-repository enablement exposed by preload.
const MCP_STATUS_META: Record<string, { label: string; className: string }> = {
  connected: { label: 'Connected', className: 'ok' },
  failed: { label: 'Failed', className: 'error' },
  'needs-auth': { label: 'Needs auth', className: 'warn' },
  pending: { label: 'Pending', className: 'warn' },
  disabled: { label: 'Disabled', className: 'muted' },
  not_configured: { label: 'Not configured', className: 'muted' },
};

const EMPTY_CATALOG: McpCatalog = { servers: {}, repoEnabled: {} };

type McpPageProps = {
  servers: McpServerInfo[];
  workspace: WorkspaceInfo;
};

type EditorState = {
  originalName: string | null;
  name: string;
  type: 'local' | 'http';
  command: string;
  args: string;
  env: string;
  cwd: string;
  url: string;
  headers: string;
  tools: string;
  timeout: string;
};

const blankEditor = (): EditorState => ({
  originalName: null,
  name: '',
  type: 'local',
  command: '',
  args: '',
  env: '',
  cwd: '',
  url: '',
  headers: '',
  tools: '*',
  timeout: '0',
});

function statusMeta(status?: string): { label: string; className: string } {
  if (!status) return { label: 'Unknown', className: 'muted' };
  return MCP_STATUS_META[status] ?? { label: status, className: 'muted' };
}

function splitList(value: string): string[] {
  return value
    .split(/\r?\n|,/)
    .map((item) => item.trim())
    .filter(Boolean);
}

function splitPairs(value: string): Record<string, string> | undefined {
  const pairs: Record<string, string> = {};
  for (const line of value.split(/\r?\n/)) {
    const trimmed = line.trim();
    if (!trimmed) continue;
    const eq = trimmed.indexOf('=');
    const sep = eq >= 0 ? eq : trimmed.indexOf(':');
    if (sep <= 0) continue;
    const key = trimmed.slice(0, sep).trim();
    if (!key) continue;
    pairs[key] = trimmed.slice(sep + 1).trim();
  }
  return Object.keys(pairs).length > 0 ? pairs : undefined;
}

function listText(values?: string[]): string {
  return values && values.length > 0 ? values.join('\n') : '';
}

function pairsText(values?: Record<string, string>): string {
  return values
    ? Object.entries(values)
        .map(([key, value]) => `${key}=${value}`)
        .join('\n')
    : '';
}

function editorFromServer(name: string, def: McpServerDef): EditorState {
  return {
    originalName: name,
    name,
    type: def.type,
    command: def.command ?? '',
    args: listText(def.args),
    env: pairsText(def.env),
    cwd: def.cwd ?? '',
    url: def.url ?? '',
    headers: pairsText(def.headers),
    tools: listText(def.tools) || '*',
    timeout: String(def.timeout ?? 0),
  };
}

function buildServerDef(editor: EditorState): McpServerDef {
  const tools = splitList(editor.tools);
  const timeout = Math.min(600, Math.max(0, Math.trunc(Number(editor.timeout) || 0)));
  const base = { type: editor.type, tools: tools.length > 0 ? tools : ['*'], timeout };

  if (editor.type === 'local') {
    const args = splitList(editor.args);
    const env = splitPairs(editor.env);
    return {
      ...base,
      type: 'local',
      command: editor.command.trim(),
      ...(args.length > 0 ? { args } : {}),
      ...(env ? { env } : {}),
      ...(editor.cwd.trim() ? { cwd: editor.cwd.trim() } : {}),
    };
  }

  const headers = splitPairs(editor.headers);
  return {
    ...base,
    type: 'http',
    url: editor.url.trim(),
    ...(headers ? { headers } : {}),
  };
}

function hasCatalogEntries(catalog: McpCatalog): boolean {
  return Object.keys(catalog.servers).length > 0 || Object.keys(catalog.repoEnabled).length > 0;
}

export function McpPage({ servers, workspace }: McpPageProps): JSX.Element {
  const [catalog, setCatalog] = useState<McpCatalog>(EMPTY_CATALOG);
  const [editor, setEditor] = useState<EditorState>(() => blankEditor());
  const [showEditor, setShowEditor] = useState(false);
  const [showRegen, setShowRegen] = useState(false);
  const [busy, setBusy] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  // Server name pending deletion (drives the confirmation modal), plus its own
  // busy/error so a failed delete keeps the modal open with the message.
  const [pendingDelete, setPendingDelete] = useState<string | null>(null);
  const [deleteBusy, setDeleteBusy] = useState(false);
  const [deleteError, setDeleteError] = useState<string | null>(null);
  const deleteModalRef = useRef<ModalHandle>(null);

  const repoKey = workspace.repoKey?.trim() ?? '';
  const catalogRows = useMemo(
    () => Object.entries(catalog.servers).sort(([a], [b]) => a.localeCompare(b)),
    [catalog.servers],
  );
  const enabledForRepo = useMemo(
    () => new Set(repoKey ? (catalog.repoEnabled[repoKey] ?? []) : []),
    [catalog.repoEnabled, repoKey],
  );

  const loadCatalog = useCallback(async () => {
    try {
      setCatalog(await window.cs.mcpRead());
      setError(null);
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    }
  }, []);

  useEffect(() => {
    let mounted = true;
    void window.cs
      .mcpRead()
      .then((next) => {
        if (mounted && hasCatalogEntries(next)) setCatalog(next);
      })
      .catch((err) => {
        if (mounted) setError(err instanceof Error ? err.message : String(err));
      });
    const unsubscribe = window.cs.onMcpChanged((next) => {
      setCatalog(next);
      setError(null);
    });
    return () => {
      mounted = false;
      unsubscribe();
    };
  }, []);

  const updateEditor = <K extends keyof EditorState>(key: K, value: EditorState[K]): void => {
    setEditor((cur) => ({ ...cur, [key]: value }));
  };

  const startAdd = (): void => {
    setEditor(blankEditor());
    setShowEditor(true);
  };

  const startEdit = (name: string, def: McpServerDef): void => {
    setEditor(editorFromServer(name, def));
    setShowEditor(true);
  };

  const submitEditor = async (event: FormEvent): Promise<void> => {
    event.preventDefault();
    const name = editor.name.trim();
    if (!name) {
      setError('Server name is required.');
      return;
    }
    if (editor.type === 'local' && !editor.command.trim()) {
      setError('Local MCP servers require a command.');
      return;
    }
    if (editor.type === 'http' && !editor.url.trim()) {
      setError('HTTP MCP servers require a URL.');
      return;
    }

    try {
      setBusy('save');
      if (editor.originalName && editor.originalName !== name) {
        await window.cs.mcpRemoveServer(editor.originalName);
      }
      setCatalog(await window.cs.mcpUpsertServer(name, buildServerDef(editor)));
      setShowEditor(false);
      setEditor(blankEditor());
      setError(null);
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setBusy(null);
    }
  };

  const confirmDelete = async (): Promise<void> => {
    if (!pendingDelete) return;
    try {
      setDeleteBusy(true);
      setDeleteError(null);
      setCatalog(await window.cs.mcpRemoveServer(pendingDelete));
      // Clear pendingDelete directly so the modal disappears even if the Modal
      // ref was already nulled by a re-render before close() could be called.
      setPendingDelete(null);
      deleteModalRef.current?.close();
    } catch (err) {
      setDeleteError(err instanceof Error ? err.message : String(err));
    } finally {
      setDeleteBusy(false);
    }
  };

  const setEnabled = async (name: string, on: boolean): Promise<void> => {
    if (!repoKey) return;
    try {
      setBusy(`toggle:${name}`);
      setCatalog(await window.cs.mcpSetEnabled(repoKey, name, on));
      setError(null);
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setBusy(null);
    }
  };

  return (
    <div className="mcp-page" aria-label="MCP servers">
      <section className="mcp-page__section" aria-labelledby="mcp-live-heading">
        <header className="mcp-page__section-head">
          <div>
            <h2 id="mcp-live-heading" className="mcp-page__title">
              Live status
            </h2>
            <p className="mcp-page__subtitle">Reported by the current agent session.</p>
          </div>
        </header>
        {servers.length === 0 ? (
          <p className="mcp-page__empty-text">No live MCP servers reported by this chat.</p>
        ) : (
          <div className="mcp-page__cards">
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
        )}
      </section>

      <section className="mcp-page__section" aria-labelledby="mcp-catalog-heading">
        <header className="mcp-page__section-head">
          <div>
            <h2 id="mcp-catalog-heading" className="mcp-page__title">
              Catalog
            </h2>
            <p className="mcp-page__subtitle">Global MCP server definitions.</p>
          </div>
          <button
            type="button"
            className="mcp-page__button mcp-page__button--primary"
            onClick={startAdd}
          >
            Add server
          </button>
        </header>

        <div className="mcp-page__apply-banner">
          <span>Changes apply to new chats.</span>
          <button type="button" className="mcp-page__button" onClick={() => setShowRegen(true)}>
            Regenerate to apply now
          </button>
        </div>

        {!repoKey && (
          <p className="mcp-page__note">
            Open a repo-backed chat to enable servers per repository.
          </p>
        )}

        {error && (
          <p className="mcp-page__error" role="alert">
            {error}
          </p>
        )}

        {catalogRows.length === 0 ? (
          <p className="mcp-page__empty-text">No global MCP servers in the catalog.</p>
        ) : (
          <div className="mcp-page__catalog-list">
            {catalogRows.map(([name, def]) => {
              const enabled = enabledForRepo.has(name);
              return (
                <article key={name} className="mcp-page__catalog-row">
                  <div className="mcp-page__catalog-main">
                    <header className="mcp-page__head">
                      <span className="mcp-page__name">{name}</span>
                      <span className="mcp-page__status mcp-page__status--muted">{def.type}</span>
                    </header>
                    <dl className="mcp-page__meta">
                      <div className="mcp-page__meta-row">
                        <dt className="mcp-page__meta-key">
                          {def.type === 'local' ? 'Command' : 'URL'}
                        </dt>
                        <dd className="mcp-page__meta-val">
                          {def.type === 'local' ? def.command : def.url}
                        </dd>
                      </div>
                      <div className="mcp-page__meta-row">
                        <dt className="mcp-page__meta-key">Tools</dt>
                        <dd className="mcp-page__meta-val">{def.tools.join(', ')}</dd>
                      </div>
                      <div className="mcp-page__meta-row">
                        <dt className="mcp-page__meta-key">Timeout</dt>
                        <dd className="mcp-page__meta-val">{def.timeout}s</dd>
                      </div>
                    </dl>
                  </div>
                  <div className="mcp-page__catalog-actions">
                    {repoKey && (
                      <label className="mcp-page__toggle">
                        <input
                          type="checkbox"
                          checked={enabled}
                          disabled={busy === `toggle:${name}`}
                          onChange={(event) => void setEnabled(name, event.currentTarget.checked)}
                        />
                        <span>{enabled ? 'Enabled' : 'Enable for repo'}</span>
                      </label>
                    )}
                    <button
                      type="button"
                      className="mcp-page__button"
                      onClick={() => startEdit(name, def)}
                    >
                      Edit
                    </button>
                    <button
                      type="button"
                      className="mcp-page__button mcp-page__button--danger"
                      onClick={() => setPendingDelete(name)}
                    >
                      Delete
                    </button>
                  </div>
                </article>
              );
            })}
          </div>
        )}
      </section>

      {showEditor && (
        <form
          className="mcp-page__editor"
          aria-label="MCP server editor"
          onSubmit={(event) => void submitEditor(event)}
        >
          <header className="mcp-page__section-head">
            <h3 className="mcp-page__title">
              {editor.originalName ? 'Edit server' : 'Add server'}
            </h3>
            <button
              type="button"
              className="mcp-page__button"
              onClick={() => {
                setShowEditor(false);
                setEditor(blankEditor());
              }}
            >
              Cancel
            </button>
          </header>

          <div className="mcp-page__form-grid">
            <label className="mcp-page__field">
              <span>Name</span>
              <input
                value={editor.name}
                onChange={(event) => updateEditor('name', event.currentTarget.value)}
              />
            </label>
            <label className="mcp-page__field">
              <span>Type</span>
              <select
                value={editor.type}
                onChange={(event) =>
                  updateEditor('type', event.currentTarget.value as 'local' | 'http')
                }
              >
                <option value="local">local</option>
                <option value="http">http</option>
              </select>
            </label>
            {editor.type === 'local' ? (
              <>
                <label className="mcp-page__field mcp-page__field--wide">
                  <span>Command</span>
                  <input
                    value={editor.command}
                    onChange={(event) => updateEditor('command', event.currentTarget.value)}
                  />
                  <small>On Windows use the full executable, e.g. agency.exe.</small>
                </label>
                <label className="mcp-page__field">
                  <span>Args</span>
                  <textarea
                    value={editor.args}
                    onChange={(event) => updateEditor('args', event.currentTarget.value)}
                    placeholder="One argument per line"
                  />
                </label>
                <label className="mcp-page__field">
                  <span>Env</span>
                  <textarea
                    value={editor.env}
                    onChange={(event) => updateEditor('env', event.currentTarget.value)}
                    placeholder="KEY=value"
                  />
                </label>
                <label className="mcp-page__field mcp-page__field--wide">
                  <span>CWD</span>
                  <input
                    value={editor.cwd}
                    onChange={(event) => updateEditor('cwd', event.currentTarget.value)}
                  />
                </label>
              </>
            ) : (
              <>
                <label className="mcp-page__field mcp-page__field--wide">
                  <span>URL</span>
                  <input
                    value={editor.url}
                    onChange={(event) => updateEditor('url', event.currentTarget.value)}
                  />
                </label>
                <label className="mcp-page__field mcp-page__field--wide">
                  <span>Headers</span>
                  <textarea
                    value={editor.headers}
                    onChange={(event) => updateEditor('headers', event.currentTarget.value)}
                    placeholder="Header-Name=value"
                  />
                </label>
              </>
            )}
            <label className="mcp-page__field">
              <span>Tools</span>
              <textarea
                value={editor.tools}
                onChange={(event) => updateEditor('tools', event.currentTarget.value)}
                placeholder="*"
              />
            </label>
            <label className="mcp-page__field">
              <span>Timeout seconds</span>
              <input
                type="number"
                min="0"
                max="600"
                value={editor.timeout}
                onChange={(event) => updateEditor('timeout', event.currentTarget.value)}
              />
            </label>
          </div>

          <div className="mcp-page__editor-actions">
            <button
              type="submit"
              className="mcp-page__button mcp-page__button--primary"
              disabled={busy === 'save'}
            >
              Save server
            </button>
            <button type="button" className="mcp-page__button" onClick={() => void loadCatalog()}>
              Refresh
            </button>
          </div>
        </form>
      )}

      {showRegen && (
        <RegenerateModal
          workspace={workspace}
          onConfirm={(handoff) => {
            void window.cs.regenerateAgent(workspace.id, handoff);
          }}
          onClose={() => setShowRegen(false)}
        />
      )}

      {pendingDelete && (
        <Modal
          ref={deleteModalRef}
          title="Delete MCP server?"
          onClose={() => {
            setPendingDelete(null);
            setDeleteError(null);
          }}
          error={deleteError}
          busy={deleteBusy}
          className="modal--confirm"
          footer={
            <>
              <button
                type="button"
                onClick={() => deleteModalRef.current?.close()}
                disabled={deleteBusy}
              >
                Cancel
              </button>
              <button
                type="button"
                className="button--primary"
                onClick={() => void confirmDelete()}
                disabled={deleteBusy}
              >
                {deleteBusy ? 'Deleting…' : 'Delete server'}
              </button>
            </>
          }
        >
          <p>
            Delete the MCP server <strong>"{pendingDelete}"</strong> from the catalog? This removes
            it for all repositories and cannot be undone.
          </p>
        </Modal>
      )}
    </div>
  );
}
