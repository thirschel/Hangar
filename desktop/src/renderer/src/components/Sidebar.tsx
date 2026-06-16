import { useEffect, useState } from 'react';
import type { CreateWorkspaceArgs } from '../../../preload';
import type { WorkspaceInfo } from '../../../main/host-client';

type SidebarProps = {
  workspaces: WorkspaceInfo[];
  selectedId: string | null;
  onSelect: (id: string) => void;
  onCreate: (args: CreateWorkspaceArgs) => Promise<void>;
  onArchive: (id: string) => Promise<void>;
  openCreateNonce?: number;
};

export function Sidebar({
  workspaces,
  selectedId,
  onSelect,
  onCreate,
  onArchive,
  openCreateNonce,
}: SidebarProps): JSX.Element {
  const [showForm, setShowForm] = useState(false);
  const [repoPath, setRepoPath] = useState('');
  const [title, setTitle] = useState('');
  const [program, setProgram] = useState('');
  const [defaultProgram, setDefaultProgram] = useState('copilot');
  const [baseBranch, setBaseBranch] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  // Open the create form when the app requests it (Ctrl+N).
  useEffect(() => {
    if (openCreateNonce && openCreateNonce > 0) setShowForm(true);
  }, [openCreateNonce]);

  // Pre-fill the agent with the daemon's default so the field is never silently
  // blank (a blank agent falls back to the config default, which can be stale).
  useEffect(() => {
    let active = true;
    window.cs
      .getDefaultProgram()
      .then((prog) => {
        if (!active) return;
        setDefaultProgram(prog);
        setProgram((cur) => cur || prog);
      })
      .catch(() => {
        /* keep the built-in 'copilot' default */
      });
    return () => {
      active = false;
    };
  }, []);

  const browse = async (): Promise<void> => {
    const dir = await window.cs.pickFolder();
    if (dir) setRepoPath(dir);
  };

  const submit = async (): Promise<void> => {
    if (!repoPath.trim() || !title.trim()) {
      setError('Repo path and title are required.');
      return;
    }
    setBusy(true);
    setError(null);
    try {
      await onCreate({
        repoPath: repoPath.trim(),
        title: title.trim(),
        program: program.trim() || undefined,
        baseBranch: baseBranch.trim() || undefined,
      });
      setShowForm(false);
      setTitle('');
      setProgram(defaultProgram);
      setBaseBranch('');
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  };

  return (
    <aside className="sidebar">
      <div className="panel-header">
        Workspaces
        <button
          className="icon-button"
          type="button"
          title="New workspace"
          onClick={() => setShowForm((s) => !s)}
        >
          +
        </button>
      </div>

      {showForm && (
        <div className="create-form">
          <label>
            Repository
            <div className="row">
              <input value={repoPath} onChange={(e) => setRepoPath(e.target.value)} placeholder="C:\path\to\repo" />
              <button type="button" onClick={browse}>
                Browse…
              </button>
            </div>
          </label>
          <label>
            Title
            <input value={title} onChange={(e) => setTitle(e.target.value)} placeholder="add-login-flow" />
          </label>
          <label>
            Agent <span className="hint">(must be on PATH)</span>
            <input value={program} onChange={(e) => setProgram(e.target.value)} placeholder={defaultProgram} />
          </label>
          <label>
            Base branch <span className="hint">(optional)</span>
            <input value={baseBranch} onChange={(e) => setBaseBranch(e.target.value)} placeholder="main" />
          </label>
          {error && <div className="form-error">{error}</div>}
          <div className="row">
            <button className="primary" type="button" onClick={submit} disabled={busy}>
              {busy ? 'Creating…' : 'Create workspace'}
            </button>
            <button type="button" onClick={() => setShowForm(false)} disabled={busy}>
              Cancel
            </button>
          </div>
        </div>
      )}

      <nav className="workspace-list" aria-label="Workspaces">
        {workspaces.length === 0 && !showForm && (
          <div className="empty-state">
            <div className="empty-state__title">No workspaces yet</div>
            <p>Click + to start a parallel agent in its own git worktree.</p>
          </div>
        )}
        {workspaces.map((w) => (
          <div
            key={w.id}
            className={`workspace-item${w.id === selectedId ? ' workspace-item--selected' : ''}`}
            onClick={() => onSelect(w.id)}
            role="button"
            tabIndex={0}
          >
            <span className={`workspace-item__dot ${w.alive ? 'is-live' : 'is-dead'}`} />
            <div className="workspace-item__body">
              <div className="workspace-item__name">{w.title}</div>
              <div className="workspace-item__detail">
                <span className="workspace-item__branch">{w.branch}</span>
                {(w.added > 0 || w.removed > 0) && (
                  <span className="diffstat">
                    <span className="add">+{w.added}</span> <span className="del">-{w.removed}</span>
                  </span>
                )}
              </div>
            </div>
            <button
              className="icon-button archive"
              type="button"
              title="Archive workspace"
              onClick={(e) => {
                e.stopPropagation();
                void onArchive(w.id);
              }}
            >
              ×
            </button>
          </div>
        ))}
      </nav>
    </aside>
  );
}
