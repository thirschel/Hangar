import type { WorkspaceInfo } from '../../../main/host-client';

type SidebarProps = {
  workspaces: WorkspaceInfo[];
  selectedId: string | null;
  onSelect: (id: string) => void;
  onArchive: (id: string) => void;
  onNewWorkspace: () => void;
};

export function Sidebar({
  workspaces,
  selectedId,
  onSelect,
  onArchive,
  onNewWorkspace,
}: SidebarProps): JSX.Element {
  return (
    <aside className="sidebar">
      <div className="panel-header">
        Workspaces
        <button
          className="icon-button"
          type="button"
          title="New workspace (Ctrl+N)"
          onClick={onNewWorkspace}
        >
          +
        </button>
      </div>

      <nav className="workspace-list" aria-label="Workspaces">
        {workspaces.length === 0 && (
          <div className="empty-state">
            <div className="empty-state__title">No workspaces yet</div>
            <p>Click + to start a parallel agent in its own git worktree.</p>
          </div>
        )}
        {workspaces.map((w) => {
          const status = !w.alive ? 'exited' : w.waiting ? 'waiting' : w.busy ? 'busy' : 'idle';
          const statusTitle =
            status === 'exited'
              ? 'Agent exited'
              : status === 'waiting'
                ? 'Waiting for input'
                : status === 'busy'
                  ? 'Working…'
                  : 'Ready';
          return (
            <div
              key={w.id}
              className={`workspace-item${w.id === selectedId ? ' workspace-item--selected' : ''}`}
              onClick={() => onSelect(w.id)}
              role="button"
              tabIndex={0}
            >
              {status === 'busy' ? (
                <span
                  className="workspace-item__spinner"
                  title={statusTitle}
                  aria-label={statusTitle}
                />
              ) : (
                <span
                  className={`workspace-item__dot is-${status}`}
                  title={statusTitle}
                  aria-label={statusTitle}
                />
              )}
              <div className="workspace-item__body">
                <div className="workspace-item__name">{w.title}</div>
                <div className="workspace-item__detail">
                  <span className="workspace-item__branch">{w.branch}</span>
                  {(w.added > 0 || w.removed > 0) && (
                    <span className="diffstat">
                      <span className="add">+{w.added}</span>{' '}
                      <span className="del">-{w.removed}</span>
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
          );
        })}
      </nav>
    </aside>
  );
}
