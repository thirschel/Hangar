import type { JSX } from 'react';
import { memo, useEffect, useMemo, useRef, useState, type ReactNode } from 'react';
import type { RefObject } from 'react';
import type { WorkspaceInfo } from '../../../main/host-client';
import { relativeTime } from '../lib/time';
import { MODE_LABELS, type SidebarMode } from './sidebar-modes';
import {
  STATUS_FILTERS,
  type StatusCounts,
  type StatusFilter,
  workspaceStatus,
} from './workspace-status';

type SidebarProps = {
  workspaces: WorkspaceInfo[];
  selectedId: string | null;
  onSelect: (id: string) => void;
  onArchive: (id: string) => void;
  onSettings: (id: string) => void;
  onNewWorkspace: () => void;
  onNewAtRepo: (repoPath: string) => void;
  onCycleMode: () => void;
  sidebarMode: SidebarMode;
  filter: string;
  onFilterChange: (value: string) => void;
  statusFilter: StatusFilter;
  counts: StatusCounts;
  onStatusFilterChange: (value: StatusFilter) => void;
  searchInputRef?: RefObject<HTMLInputElement | null>;
  // Optional labels so the same sidebar reads naturally for both surfaces: the
  // standard view keeps "Workspaces", the agent view passes "Chats" / "chat".
  // Defaults preserve the standard wording.
  title?: string;
  noun?: string;
  emptyHint?: ReactNode;
  // Grid multi-select (optional so callers that don't use the grid stay simple).
  // Set of workspace ids currently chosen for the multi-agent grid view.
  gridSelectedIds?: ReadonlySet<string>;
  // Toggle a workspace in/out of the grid selection.
  onToggleGridMember?: (id: string) => void;
  // Clear the entire grid selection.
  onClearGridSelection?: () => void;
  // Ids whose removal is in flight: their rows show a "deleting" status and a
  // click shows a "this <noun> is deleting" hint instead of selecting.
  deletingIds?: ReadonlySet<string>;
};

type WorkspaceRowProps = {
  w: WorkspaceInfo;
  selected: boolean;
  relTime: string;
  onSelect: (id: string) => void;
  onArchive: (id: string) => void;
  onSettings: (id: string) => void;
  // Whether this row is currently part of the grid selection.
  gridSelected?: boolean;
  // Provided only when grid selection is enabled; toggles this row's membership.
  onToggleGrid?: (id: string) => void;
  // True while this row's removal is in flight: it renders a muted "Deleting…"
  // status, hides its actions, and a click shows a transient hint instead of
  // selecting it.
  deleting?: boolean;
  // Singular label used in the deleting hint ("This chat is deleting").
  noun?: string;
};

function WorkspaceRowImpl({
  w,
  selected,
  relTime,
  onSelect,
  onArchive,
  onSettings,
  gridSelected,
  onToggleGrid,
  deleting = false,
  noun = 'workspace',
}: WorkspaceRowProps): JSX.Element {
  const status = workspaceStatus(w);
  const statusTitle =
    status === 'exited'
      ? 'Agent exited'
      : status === 'waiting'
        ? 'Waiting for input'
        : status === 'busy'
          ? 'Working…'
          : 'Ready';

  // Transient "this <noun> is deleting" hint shown when a deleting row is
  // clicked. Ref-based timeout so repeated clicks reset the auto-hide cleanly.
  const [hintShown, setHintShown] = useState(false);
  const hintTimer = useRef<ReturnType<typeof setTimeout> | null>(null);
  useEffect(
    () => () => {
      if (hintTimer.current) clearTimeout(hintTimer.current);
    },
    [],
  );

  const handleClick = (): void => {
    if (deleting) {
      setHintShown(true);
      if (hintTimer.current) clearTimeout(hintTimer.current);
      hintTimer.current = setTimeout(() => setHintShown(false), 1800);
      return;
    }
    onSelect(w.id);
  };

  return (
    <div
      className={`workspace-item${selected ? ' workspace-item--selected' : ''}${
        onToggleGrid && !deleting ? ' workspace-item--selectable' : ''
      }${deleting ? ' workspace-item--deleting' : ''}`}
      onClick={handleClick}
      role="button"
      tabIndex={0}
      aria-disabled={deleting || undefined}
    >
      {onToggleGrid && !deleting && (
        <span className="workspace-item__grid-slot">
          {w.alive && (
            <input
              type="checkbox"
              className="workspace-item__grid-check"
              checked={!!gridSelected}
              aria-label={`Add ${w.title} to grid`}
              onClick={(e) => e.stopPropagation()}
              onChange={(e) => {
                e.stopPropagation();
                onToggleGrid(w.id);
              }}
            />
          )}
        </span>
      )}
      {deleting ? (
        <span
          className="workspace-item__spinner workspace-item__spinner--deleting"
          title="Deleting…"
          aria-label="Deleting…"
        />
      ) : status === 'busy' ? (
        <span className="workspace-item__spinner" title={statusTitle} aria-label={statusTitle} />
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
          {deleting ? (
            <span className="workspace-item__deleting-label">Deleting…</span>
          ) : (
            <>
              <span className="workspace-item__branch">{w.branch}</span>
              {w.hasWorktree && (
                <span
                  className="workspace-item__worktree"
                  title="Isolated git worktree"
                  aria-label="Isolated git worktree"
                >
                  ⎇
                </span>
              )}
              {(w.added > 0 || w.removed > 0) && (
                <span className="diffstat">
                  <span className="add">+{w.added}</span> <span className="del">-{w.removed}</span>
                </span>
              )}
              {relTime && (
                <span className="workspace-item__time" title="Last agent output">
                  {relTime}
                </span>
              )}
            </>
          )}
        </div>
      </div>
      {!deleting && (
        <div className="workspace-item__actions">
          <button
            className="icon-button archive"
            type="button"
            title="Archive workspace (D)"
            onClick={(e) => {
              e.stopPropagation();
              void onArchive(w.id);
            }}
          >
            ×
          </button>
          <button
            className="icon-button workspace-settings"
            type="button"
            title="Workspace settings"
            onClick={(e) => {
              e.stopPropagation();
              onSettings(w.id);
            }}
          >
            ⚙
          </button>
        </div>
      )}
      {deleting && hintShown && (
        <span className="workspace-item__deleting-hint" role="status">
          This {noun} is deleting
        </span>
      )}
    </div>
  );
}

/**
 * Rows are recreated on every poll because the workspace list is replaced
 * wholesale, so a default (shallow-equal) memo would never hit — the `w` object
 * identity changes each refresh. Compare the fields the row actually renders
 * plus its (now stable) handlers, so an unchanged row skips re-rendering when an
 * unrelated workspace updates.
 */
const WorkspaceRow = memo(WorkspaceRowImpl, (prev, next) => {
  return (
    prev.selected === next.selected &&
    prev.relTime === next.relTime &&
    prev.onSelect === next.onSelect &&
    prev.onArchive === next.onArchive &&
    prev.onSettings === next.onSettings &&
    prev.gridSelected === next.gridSelected &&
    prev.onToggleGrid === next.onToggleGrid &&
    prev.deleting === next.deleting &&
    prev.noun === next.noun &&
    prev.w.id === next.w.id &&
    prev.w.title === next.w.title &&
    prev.w.branch === next.w.branch &&
    prev.w.hasWorktree === next.w.hasWorktree &&
    prev.w.added === next.w.added &&
    prev.w.removed === next.w.removed &&
    prev.w.alive === next.w.alive &&
    prev.w.waiting === next.w.waiting &&
    prev.w.busy === next.w.busy &&
    prev.w.lastOutputUnix === next.w.lastOutputUnix
  );
});

const STATUS_LABELS: Record<StatusFilter, string> = {
  all: 'All',
  waiting: 'Waiting',
  busy: 'Busy',
  idle: 'Idle',
  exited: 'Exited',
};

function StatusFilterBar({
  active,
  counts,
  onChange,
}: {
  active: StatusFilter;
  counts: StatusCounts;
  onChange: (value: StatusFilter) => void;
}): JSX.Element {
  return (
    <div className="status-filter-bar" aria-label="Filter workspaces by status">
      {STATUS_FILTERS.map((status) => (
        <button
          key={status}
          className={`status-chip status-chip--${status}${active === status ? ' is-active' : ''}`}
          type="button"
          title={`Show ${STATUS_LABELS[status].toLowerCase()} workspaces`}
          aria-pressed={active === status}
          onClick={() => onChange(status)}
        >
          <span className="status-chip__label">{STATUS_LABELS[status]}</span>
          <span className="status-chip__count">{counts[status]}</span>
        </button>
      ))}
    </div>
  );
}

function SectionHeader({
  label,
  onAdd,
}: {
  label: string;
  onAdd?: () => void;
}): JSX.Element {
  return (
    <div className="sidebar-section-header">
      <span className="sidebar-section-header__label">{label}</span>
      <span className="sidebar-section-header__line" />
      {onAdd && (
        <button
          className="icon-button sidebar-section-header__add"
          type="button"
          title={`New workspace in ${label}`}
          onClick={onAdd}
        >
          +
        </button>
      )}
    </div>
  );
}

/**
 * Compute the "last agent output" relative label for a row. This is done here,
 * outside the memoized WorkspaceRow, and passed in as a prop so that an idle
 * workspace whose other fields never change still re-renders when the bucket
 * rolls over ("5m ago" -> "6m ago"): the parent re-polls every uiRefreshMs
 * (~2s) producing a fresh string, which flows through the row's memo comparator.
 * Returns '' when there is no known output time (so the row renders nothing).
 */
function rowRelTime(w: WorkspaceInfo): string {
  return w.lastOutputUnix > 0 ? relativeTime(w.lastOutputUnix) : '';
}

function buildGroupedList(
  workspaces: WorkspaceInfo[],
  mode: SidebarMode,
  selectedId: string | null,
  onSelect: (id: string) => void,
  onArchive: (id: string) => void,
  onSettings: (id: string) => void,
  onNewAtRepo?: (repoPath: string) => void,
  gridSelectedIds?: ReadonlySet<string>,
  onToggleGridMember?: (id: string) => void,
  deletingIds?: ReadonlySet<string>,
  noun?: string,
): ReactNode[] {
  const renderRow = (w: WorkspaceInfo): JSX.Element => (
    <WorkspaceRow
      key={w.id}
      w={w}
      selected={w.id === selectedId}
      relTime={rowRelTime(w)}
      onSelect={onSelect}
      onArchive={onArchive}
      onSettings={onSettings}
      gridSelected={gridSelectedIds?.has(w.id) ?? false}
      onToggleGrid={onToggleGridMember}
      deleting={deletingIds?.has(w.id) ?? false}
      noun={noun}
    />
  );

  if (mode === 'group-by-repo') {
    const groups = new Map<string, WorkspaceInfo[]>();
    for (const w of workspaces) {
      const repo = w.repoPath || 'Unknown';
      if (!groups.has(repo)) groups.set(repo, []);
      groups.get(repo)!.push(w);
    }
    const nodes: ReactNode[] = [];
    for (const [repo, items] of groups) {
      // Show just the last path segment as the repo name.
      const repoName = repo.split(/[\\/]/).pop() || repo;
      nodes.push(
        <SectionHeader
          key={`hdr-${repo}`}
          label={repoName}
          onAdd={onNewAtRepo ? () => onNewAtRepo(repo) : undefined}
        />,
      );
      for (const w of items) {
        nodes.push(renderRow(w));
      }
    }
    return nodes;
  }

  if (mode === 'pinned-pending') {
    const pending = workspaces.filter((w) => w.waiting);
    const rest = workspaces.filter((w) => !w.waiting);
    const nodes: ReactNode[] = [];
    if (pending.length > 0) {
      nodes.push(<SectionHeader key="hdr-pending" label="Pending" />);
      for (const w of pending) {
        nodes.push(renderRow(w));
      }
    }
    if (rest.length > 0) {
      nodes.push(<SectionHeader key="hdr-other" label="Other" />);
      for (const w of rest) {
        nodes.push(renderRow(w));
      }
    }
    return nodes;
  }

  // Flat list for manual / recent-activity.
  return workspaces.map((w) => renderRow(w));
}

function SidebarImpl({
  workspaces,
  selectedId,
  onSelect,
  onArchive,
  onSettings,
  onNewWorkspace,
  onNewAtRepo,
  onCycleMode,
  sidebarMode,
  filter,
  onFilterChange,
  statusFilter,
  counts,
  onStatusFilterChange,
  searchInputRef,
  title = 'Workspaces',
  noun = 'workspace',
  emptyHint,
  gridSelectedIds,
  onToggleGridMember,
  onClearGridSelection,
  deletingIds,
}: SidebarProps): JSX.Element {
  const internalRef = useRef<HTMLInputElement>(null);
  const inputRef = searchInputRef ?? internalRef;

  // Build the sorted/grouped row elements once and cache until a relevant prop
  // changes. This memo + the React.memo wrapper on Sidebar itself prevent the
  // full row list from being rebuilt on every 2s poll when nothing changed.
  const groupedList = useMemo(
    () =>
      workspaces.length > 0
        ? buildGroupedList(
            workspaces,
            sidebarMode,
            selectedId,
            onSelect,
            onArchive,
            onSettings,
            onNewAtRepo,
            gridSelectedIds,
            onToggleGridMember,
            deletingIds,
            noun,
          )
        : null,
    [
      workspaces,
      sidebarMode,
      selectedId,
      onSelect,
      onArchive,
      onSettings,
      onNewAtRepo,
      gridSelectedIds,
      onToggleGridMember,
      deletingIds,
      noun,
    ],
  );

  return (
    <aside className="sidebar">
      <div className="panel-header">
        <span className="sidebar__title">
          {title}
          <span className="sidebar__mode-label">{MODE_LABELS[sidebarMode]}</span>
        </span>
        <div className="panel-header__actions">
          <button
            className="icon-button"
            type="button"
            title={`Sort: ${MODE_LABELS[sidebarMode]} (s)`}
            onClick={onCycleMode}
          >
            ⇅
          </button>
          <button
            className="icon-button"
            type="button"
            title={`New ${noun} (n)`}
            onClick={onNewWorkspace}
          >
            +
          </button>
        </div>
      </div>

      {onToggleGridMember && gridSelectedIds && gridSelectedIds.size > 0 && (
        <div className="sidebar__grid-selection">
          <span className="sidebar__grid-selection-count">{gridSelectedIds.size} selected</span>
          <button
            className="sidebar__grid-selection-clear"
            type="button"
            onClick={onClearGridSelection}
          >
            Clear
          </button>
        </div>
      )}

      <div className="sidebar-search">
        <input
          ref={inputRef}
          className="sidebar-search__input"
          type="text"
          placeholder={`Filter ${noun}s… (/)`}
          value={filter}
          onChange={(e) => onFilterChange(e.target.value)}
          data-is-input="true"
        />
      </div>
      <StatusFilterBar active={statusFilter} counts={counts} onChange={onStatusFilterChange} />

      <nav className="workspace-list" aria-label={title}>
        {workspaces.length === 0 && !filter && statusFilter === 'all' && (
          <div className="empty-state">
            <div className="empty-state__title">No {noun}s yet</div>
            {emptyHint ?? (
              <p>
                Click + to start a parallel agent — in its own git worktree, or in-place in a folder
                you pick.
              </p>
            )}
          </div>
        )}
        {workspaces.length === 0 && (filter || statusFilter !== 'all') && (
          <div className="empty-state">
            <div className="empty-state__title">No matches</div>
            <p>
              No {noun}s match
              {filter ? <> &ldquo;{filter}&rdquo;</> : null}
              {statusFilter !== 'all' ? ` in ${STATUS_LABELS[statusFilter].toLowerCase()}` : null}
            </p>
          </div>
        )}
        {workspaces.length > 0 && groupedList}
      </nav>
    </aside>
  );
}

/**
 * Element-wise comparison of the workspace array on the fields that Sidebar
 * actually renders. WorkspaceInfo objects are replaced wholesale each poll, so
 * default reference equality always fails; this comparator lets Sidebar skip
 * re-rendering when nothing changed.
 */
function workspacesEqualForMemo(a: WorkspaceInfo[], b: WorkspaceInfo[]): boolean {
  if (a.length !== b.length) return false;
  for (let i = 0; i < a.length; i++) {
    const p = a[i];
    const n = b[i];
    if (
      p.id !== n.id ||
      p.title !== n.title ||
      p.alive !== n.alive ||
      p.busy !== n.busy ||
      p.waiting !== n.waiting ||
      p.added !== n.added ||
      p.removed !== n.removed ||
      p.regenerating !== n.regenerating ||
      p.lastOutputUnix !== n.lastOutputUnix
    )
      return false;
  }
  return true;
}

export const Sidebar = memo(
  SidebarImpl,
  (prev, next) =>
    workspacesEqualForMemo(prev.workspaces, next.workspaces) &&
    prev.selectedId === next.selectedId &&
    prev.filter === next.filter &&
    prev.statusFilter === next.statusFilter &&
    prev.sidebarMode === next.sidebarMode &&
    prev.counts === next.counts &&
    prev.gridSelectedIds === next.gridSelectedIds &&
    prev.deletingIds === next.deletingIds &&
    prev.title === next.title &&
    prev.noun === next.noun &&
    prev.onSelect === next.onSelect &&
    prev.onArchive === next.onArchive &&
    prev.onSettings === next.onSettings &&
    prev.onNewWorkspace === next.onNewWorkspace &&
    prev.onNewAtRepo === next.onNewAtRepo &&
    prev.onCycleMode === next.onCycleMode &&
    prev.onFilterChange === next.onFilterChange &&
    prev.onStatusFilterChange === next.onStatusFilterChange &&
    prev.onToggleGridMember === next.onToggleGridMember &&
    prev.onClearGridSelection === next.onClearGridSelection,
);
