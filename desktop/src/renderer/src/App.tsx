import type { JSX } from 'react';
import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { CenterPane } from './components/CenterPane';
import { RightPanel } from './components/RightPanel';
import { Sidebar } from './components/Sidebar';
import { GridPane } from './components/GridPane';
import { AgentMode } from './components/AgentMode';
import { SIDEBAR_MODES, type SidebarMode } from './components/sidebar-modes';
import { BreadcrumbCopy } from './components/BreadcrumbCopy';
import { CreateWorkspaceModal } from './components/CreateWorkspaceModal';
import { SettingsModal } from './components/SettingsModal';
import { RegenerateModal } from './components/RegenerateModal';
import { RemoveWorkspaceModal } from './components/RemoveWorkspaceModal';
import { WorkspaceSettingsModal } from './components/WorkspaceSettingsModal';
import { SessionBrowserModal } from './components/SessionBrowserModal';
import { WelcomeModal } from './components/WelcomeModal';
import { HelpModal } from './components/HelpModal';
import {
  countByStatus,
  filterByStatus,
  isStatusFilter,
  nextStatusFilter,
  type StatusFilter,
} from './components/workspace-status';
import { playNotificationSound } from './notificationSound';
import { diag } from './diag';
import type { CreateWorkspaceArgs } from '../../preload';
import type { WorkspaceInfo } from '../../main/host-client';
import { PROTO_VERSION } from '../../shared/proto-version';

type ConnectionState = 'connecting' | 'connected' | 'error';

// App-level experience mode: the existing workspace UI ('standard') or the
// full-screen Copilot Agent surface ('agent'). Persisted across launches.
type AppMode = 'standard' | 'agent';

// Right-panel resize bounds (the panel width is user-draggable and persisted).
const SIDE_MIN = 320;
const SIDE_DEFAULT = 420;
const SIDE_KEY = 'cs.sideWidth';
const SIDEBAR_W = 280;
const CENTER_MIN = 360;
const GUTTER_W = 6;

// Sidebar state persistence keys.
const SIDEBAR_MODE_KEY = 'cs.sidebarMode';
const SIDEBAR_ORDER_KEY = 'cs.workspaceOrder';
const STATUS_FILTER_KEY = 'cs.statusFilter';
const GRID_COLUMNS_KEY = 'cs.gridColumns';
const GRID_ORDER_KEY = 'cs.gridOrder';
const GRID_ROW_HEIGHTS_KEY = 'cs.gridRowHeights';

// App-mode persistence key.
const APP_MODE_KEY = 'cs.appMode';

// Largest the right panel may grow to for the current window, keeping the sidebar
// and a usable center pane visible.
function sideMax(): number {
  return Math.max(
    SIDE_MIN,
    Math.min(window.innerWidth * 0.7, window.innerWidth - SIDEBAR_W - CENTER_MIN - GUTTER_W),
  );
}


export function App(): JSX.Element {
  const [connection, setConnection] = useState<ConnectionState>('connecting');
  const [hostVersion, setHostVersion] = useState<number | null>(null);
  const [statusText, setStatusText] = useState('connecting to session-host…');
  const [workspaces, setWorkspaces] = useState<WorkspaceInfo[]>([]);
  const [selectedId, setSelectedId] = useState<string | null>(null);
  const [showSettings, setShowSettings] = useState(false);
  const [showCreate, setShowCreate] = useState(false);
  const [createRepoPath, setCreateRepoPath] = useState<string | undefined>();
  const [showRegen, setShowRegen] = useState(false);
  const [optimisticRegenId, setOptimisticRegenId] = useState<string | null>(null);
  const [workspaceToRemove, setWorkspaceToRemove] = useState<WorkspaceInfo | null>(null);
  const [workspaceToEdit, setWorkspaceToEdit] = useState<WorkspaceInfo | null>(null);
  const [showBrowser, setShowBrowser] = useState(false);
  const [showWelcome, setShowWelcome] = useState(false);
  const [refreshMs, setRefreshMs] = useState(2000);
  const [sideWidth, setSideWidth] = useState<number>(() => {
    const saved = Number(localStorage.getItem(SIDE_KEY));
    return Number.isFinite(saved) && saved >= SIDE_MIN ? saved : SIDE_DEFAULT;
  });
  const [sidebarMode, setSidebarMode] = useState<SidebarMode>(() => {
    const saved = localStorage.getItem(SIDEBAR_MODE_KEY);
    return SIDEBAR_MODES.includes(saved as SidebarMode) ? (saved as SidebarMode) : 'manual';
  });
  const [sidebarFilter, setSidebarFilter] = useState('');
  const [statusFilter, setStatusFilter] = useState<StatusFilter>(() => {
    const saved = localStorage.getItem(STATUS_FILTER_KEY);
    return isStatusFilter(saved) ? saved : 'all';
  });
  const [workspaceOrder, setWorkspaceOrder] = useState<string[]>(() => {
    try {
      const arr = JSON.parse(localStorage.getItem(SIDEBAR_ORDER_KEY) ?? '[]') as unknown;
      return Array.isArray(arr) ? (arr as string[]) : [];
    } catch {
      return [];
    }
  });
  const [showHelp, setShowHelp] = useState(false);
  const [appMode, setAppMode] = useState<AppMode>(() => {
    const saved = localStorage.getItem(APP_MODE_KEY);
    return saved === 'agent' ? 'agent' : 'standard';
  });
  const [gridMode, setGridMode] = useState(false);
  const [gridSelectedIds, setGridSelectedIds] = useState<Set<string>>(() => new Set());
  const [gridColumns, setGridColumns] = useState<number>(() => {
    const v = Number(localStorage.getItem(GRID_COLUMNS_KEY));
    return Number.isFinite(v) && v > 0 ? Math.floor(v) : 0;
  });
  const [gridOrder, setGridOrder] = useState<string[]>(() => {
    try {
      const arr = JSON.parse(localStorage.getItem(GRID_ORDER_KEY) ?? '[]');
      return Array.isArray(arr) ? arr.filter((x): x is string => typeof x === 'string') : [];
    } catch {
      return [];
    }
  });
  const [gridRowHeights, setGridRowHeights] = useState<number[]>(() => {
    try {
      const arr = JSON.parse(localStorage.getItem(GRID_ROW_HEIGHTS_KEY) ?? '[]');
      return Array.isArray(arr) ? arr.filter((x): x is number => typeof x === 'number') : [];
    } catch {
      return [];
    }
  });
  const ready = useRef(false);
  const workspacesRef = useRef<WorkspaceInfo[]>([]);
  const gridSelectedIdsRef = useRef<Set<string>>(new Set());
  const aliveRef = useRef<Map<string, boolean>>(new Map());
  const sessionNameRef = useRef<Map<string, string>>(new Map());
  const regeneratingRef = useRef<Map<string, boolean>>(new Map());
  const waitingRef = useRef<Map<string, boolean>>(new Map());
  const connectionRef = useRef<ConnectionState>('connecting');
  const searchInputRef = useRef<HTMLInputElement>(null);
  const refreshInFlightRef = useRef<Promise<WorkspaceInfo[]> | null>(null);

  const refreshOnce = useCallback(async (): Promise<WorkspaceInfo[]> => {
    try {
      const list = await window.cs.listWorkspaces();
      // Notify when an agent session goes alive -> not alive (i.e. it exited).
      const prevAlive = aliveRef.current;
      const prevSessionName = sessionNameRef.current;
      const prevRegenerating = regeneratingRef.current;
      if (prevAlive.size > 0) {
        for (const w of list) {
          const sessionChanged =
            prevSessionName.has(w.id) && prevSessionName.get(w.id) !== w.sessionName;
          const inRegenWindow = w.regenerating || prevRegenerating.get(w.id) === true;
          if (prevAlive.get(w.id) === true && !w.alive && !sessionChanged && !inRegenWindow) {
            void window.cs.notify({ title: 'Agent finished', body: w.title, workspaceId: w.id });
          }
        }
      }
      const nextAlive = new Map<string, boolean>();
      const nextSessionName = new Map<string, string>();
      const nextRegenerating = new Map<string, boolean>();
      const nextWaiting = new Map<string, boolean>();
      for (const w of list) {
        nextAlive.set(w.id, w.alive);
        nextSessionName.set(w.id, w.sessionName);
        nextRegenerating.set(w.id, w.regenerating);
        nextWaiting.set(w.id, w.waiting);
      }

      // Notify when a workspace transitions to "waiting for input".
      const prevWaiting = waitingRef.current;
      if (prevWaiting.size > 0) {
        for (const w of list) {
          if (w.waiting && !prevWaiting.get(w.id)) {
            void window.cs.notify({ title: 'Agent needs input', body: w.title, workspaceId: w.id });
          }
        }
      }

      // Update taskbar badge with total waiting count.
      const waitingCount = list.filter((w) => w.waiting).length;
      void window.cs.setBadge(waitingCount);

      aliveRef.current = nextAlive;
      sessionNameRef.current = nextSessionName;
      regeneratingRef.current = nextRegenerating;
      waitingRef.current = nextWaiting;
      setWorkspaces(list);
      // Recover the status banner if a previous poll had errored (e.g. the daemon
      // restarted and the control pipe reconnected).
      if (connectionRef.current !== 'connected') {
        setConnection('connected');
        setStatusText('connected · daemon live');
      }
      return list;
    } catch (error) {
      setConnection('error');
      setStatusText(`error · ${error instanceof Error ? error.message : String(error)}`);
      return [];
    }
  }, []);

  // Deduplicate concurrent refreshes: the workspace list is polled on an interval
  // and also refreshed after actions, and a single ListWorkspaces can be slow
  // (the daemon computes per-workspace git diff). Without this guard a slow call
  // lets every subsequent tick enqueue another request on the one control
  // connection, starving other RPCs (e.g. the session browser). While a refresh
  // is in flight, additional callers share its result instead of piling on.
  const refresh = useCallback((): Promise<WorkspaceInfo[]> => {
    if (refreshInFlightRef.current) {
      return refreshInFlightRef.current;
    }
    const p = refreshOnce();
    refreshInFlightRef.current = p;
    void p.finally(() => {
      refreshInFlightRef.current = null;
    });
    return p;
  }, [refreshOnce]);

  useEffect(() => {
    let active = true;
    void (async () => {
      try {
        const hello = await window.cs.call({ method: 'Hello', clientVersion: PROTO_VERSION });
        if (!active) return;
        const hv = hello.hostVersion ?? 0;
        setHostVersion(hv);
        if (hv !== PROTO_VERSION) {
          setConnection('error');
          setStatusText(
            `daemon is v${hv} — the desktop app needs v${PROTO_VERSION}. Run \`cs reset\`, then relaunch.`,
          );
          return;
        }
        setConnection('connected');
        setStatusText(`connected · daemon v${hv}`);
        ready.current = true;
        await refresh();
      } catch (error) {
        if (!active) return;
        setConnection('error');
        setStatusText(
          `cannot reach session-host · ${error instanceof Error ? error.message : String(error)}`,
        );
      }
    })();

    window.cs
      .getSettings()
      .then((s) => {
        if (active) setRefreshMs(s.uiRefreshMs);
      })
      .catch(() => {});
    const unsubFocus = window.cs.onFocusWorkspace((id) => setSelectedId(id));
    const unsubWelcome = window.cs.onFirstRun?.(() => setShowWelcome(true));
    const unsubSound = window.cs.onPlayNotificationSound?.(() => playNotificationSound());

    return () => {
      active = false;
      unsubFocus();
      unsubWelcome?.();
      unsubSound?.();
    };
  }, [refresh]);

  // Poll the workspace list at the configured interval.
  useEffect(() => {
    const timer = setInterval(() => {
      if (ready.current) void refresh();
    }, refreshMs);
    return () => clearInterval(timer);
  }, [refreshMs, refresh]);

  // App keyboard shortcuts.
  useEffect(() => {
    const isInputFocused = (): boolean => {
      const el = document.activeElement;
      if (!el) return false;
      const tag = el.tagName.toLowerCase();
      if (tag === 'input' || tag === 'textarea') return true;
      // xterm terminal helper textarea
      if (el.classList.contains('xterm-helper-textarea')) return true;
      if (el.closest('[data-is-input]')) return true;
      return false;
    };

    const onKey = (e: KeyboardEvent): void => {
      // Modifier shortcuts always work (even with input focused).
      if (e.ctrlKey && e.key === ',') {
        e.preventDefault();
        setShowSettings((s) => !s);
        return;
      }
      if (e.ctrlKey && (e.key === 'n' || e.key === 'N')) {
        e.preventDefault();
        setCreateRepoPath(undefined);
        setShowCreate(true);
        return;
      }
      if (e.altKey && e.key >= '1' && e.key <= '9') {
        const w = workspacesRef.current[Number(e.key) - 1];
        if (w) {
          e.preventDefault();
          setSelectedId(w.id);
        }
        return;
      }

      // Bare-key shortcuts only fire when no text input/terminal has focus.
      if (isInputFocused()) return;

      const ws = workspacesRef.current;

      switch (e.key) {
        case 'n':
          e.preventDefault();
          setCreateRepoPath(undefined);
          setShowCreate(true);
          break;
        case 'N':
          e.preventDefault();
          setCreateRepoPath(undefined);
          setShowCreate(true);
          break;
        case 'q':
          e.preventDefault();
          window.close();
          break;
        case '?':
          e.preventDefault();
          setShowHelp((h) => !h);
          break;
        case 'b':
          e.preventDefault();
          setShowBrowser(true);
          break;
        case 'g':
          e.preventDefault();
          setGridMode((g) => (g ? false : gridSelectedIdsRef.current.size >= 2));
          break;
        case '/':
          e.preventDefault();
          searchInputRef.current?.focus();
          break;
        case 'f':
        case 'F':
          e.preventDefault();
          setStatusFilter((cur) => {
            const next = nextStatusFilter(cur);
            localStorage.setItem(STATUS_FILTER_KEY, next);
            return next;
          });
          break;
        case 'Escape':
          e.preventDefault();
          if (document.activeElement === searchInputRef.current) {
            setSidebarFilter('');
            searchInputRef.current?.blur();
          }
          break;
        case 's': {
          e.preventDefault();
          setSidebarMode((cur) => {
            const idx = SIDEBAR_MODES.indexOf(cur);
            const next = SIDEBAR_MODES[(idx + 1) % SIDEBAR_MODES.length];
            localStorage.setItem(SIDEBAR_MODE_KEY, next);
            return next;
          });
          break;
        }
        case 'S': {
          e.preventDefault();
          setSidebarMode((cur) => {
            const idx = SIDEBAR_MODES.indexOf(cur);
            const next = SIDEBAR_MODES[(idx - 1 + SIDEBAR_MODES.length) % SIDEBAR_MODES.length];
            localStorage.setItem(SIDEBAR_MODE_KEY, next);
            return next;
          });
          break;
        }
        case 'j':
        case 'ArrowDown': {
          e.preventDefault();
          setSelectedId((cur) => {
            const idx = ws.findIndex((w) => w.id === cur);
            if (idx < ws.length - 1) return ws[idx + 1].id;
            if (idx === -1 && ws.length > 0) return ws[0].id;
            return cur;
          });
          break;
        }
        case 'k':
        case 'ArrowUp': {
          e.preventDefault();
          setSelectedId((cur) => {
            const idx = ws.findIndex((w) => w.id === cur);
            if (idx > 0) return ws[idx - 1].id;
            if (idx === -1 && ws.length > 0) return ws[ws.length - 1].id;
            return cur;
          });
          break;
        }
        case 'J': {
          e.preventDefault();
          if (sidebarMode !== 'manual') break;
          setWorkspaceOrder((order) => {
            const ids = order.length > 0 ? order : ws.map((w) => w.id);
            const idx = ids.indexOf(selectedId ?? '');
            if (idx < 0 || idx >= ids.length - 1) return order;
            const next = [...ids];
            [next[idx], next[idx + 1]] = [next[idx + 1], next[idx]];
            localStorage.setItem(SIDEBAR_ORDER_KEY, JSON.stringify(next));
            return next;
          });
          break;
        }
        case 'K': {
          e.preventDefault();
          if (sidebarMode !== 'manual') break;
          setWorkspaceOrder((order) => {
            const ids = order.length > 0 ? order : ws.map((w) => w.id);
            const idx = ids.indexOf(selectedId ?? '');
            if (idx <= 0) return order;
            const next = [...ids];
            [next[idx], next[idx - 1]] = [next[idx - 1], next[idx]];
            localStorage.setItem(SIDEBAR_ORDER_KEY, JSON.stringify(next));
            return next;
          });
          break;
        }
        case 'p': {
          e.preventDefault();
          const sel = ws.find((w) => w.id === selectedId);
          if (sel && sel.branch && (sel.added > 0 || sel.removed > 0)) {
            void window.cs.pushWorkspace(sel.id);
          }
          break;
        }
        case 'D': {
          e.preventDefault();
          const sel = ws.find((w) => w.id === selectedId);
          if (sel) setWorkspaceToRemove(sel);
          break;
        }
        default:
          break;
      }
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [selectedId, sidebarMode]);

  workspacesRef.current = workspaces;
  connectionRef.current = connection;

  // Derive the displayed workspace list by applying mode sorting, custom order, and filter.
  const statusCounts = useMemo(() => countByStatus(workspaces), [workspaces]);
  const displayedWorkspaces = useMemo(() => {
    let list = [...workspaces];

    // Apply mode-based sorting.
    switch (sidebarMode) {
      case 'manual':
        if (workspaceOrder.length > 0) {
          const orderMap = new Map(workspaceOrder.map((id, i) => [id, i]));
          list.sort((a, b) => {
            const ai = orderMap.get(a.id) ?? Infinity;
            const bi = orderMap.get(b.id) ?? Infinity;
            return ai - bi;
          });
        }
        break;
      case 'group-by-repo':
        list.sort((a, b) => a.repoPath.localeCompare(b.repoPath) || a.title.localeCompare(b.title));
        break;
      case 'recent-activity':
        list.sort((a, b) => b.createdUnix - a.createdUnix);
        break;
      case 'pinned-pending':
        list.sort((a, b) => {
          const aw = a.waiting ? 0 : 1;
          const bw = b.waiting ? 0 : 1;
          return aw - bw || a.title.localeCompare(b.title);
        });
        break;
    }

    // Apply search filter.
    if (sidebarFilter) {
      const q = sidebarFilter.toLowerCase();
      list = list.filter(
        (w) => w.title.toLowerCase().includes(q) || w.branch.toLowerCase().includes(q),
      );
    }

    list = filterByStatus(list, statusFilter);

    return list;
  }, [workspaces, sidebarMode, workspaceOrder, sidebarFilter, statusFilter]);

  // Keep workspacesRef in sync with display order for hotkey navigation.
  workspacesRef.current = displayedWorkspaces;

  const selected = workspaces.find((w) => w.id === selectedId) ?? null;
  const selectedRegenerating =
    (selected?.regenerating ?? false) ||
    (optimisticRegenId !== null && optimisticRegenId === selected?.id);

  // Keep a ref to the current grid selection for the keydown handler (subscribed
  // with a narrow deps list).
  gridSelectedIdsRef.current = gridSelectedIds;

  // The selected agents to tile: ordered by the user's drag-and-drop order
  // (gridOrder), with any newly-selected agents appended in sidebar display order.
  const gridWorkspaces = useMemo(() => {
    const selected = displayedWorkspaces.filter((w) => gridSelectedIds.has(w.id));
    const byId = new Map(selected.map((w) => [w.id, w]));
    const ranked = gridOrder
      .map((id) => byId.get(id))
      .filter((w): w is WorkspaceInfo => w !== undefined);
    const rankedIds = new Set(ranked.map((w) => w.id));
    const unranked = selected.filter((w) => !rankedIds.has(w.id));
    return [...ranked, ...unranked];
  }, [displayedWorkspaces, gridSelectedIds, gridOrder]);

  // Drop archived/removed workspaces from the grid selection.
  useEffect(() => {
    setGridSelectedIds((prev) => {
      if (prev.size === 0) return prev;
      const present = new Set(workspaces.map((w) => w.id));
      let changed = false;
      const next = new Set<string>();
      for (const id of prev) {
        if (present.has(id)) next.add(id);
        else changed = true;
      }
      return changed ? next : prev;
    });
  }, [workspaces]);

  // Leave grid mode if there is nothing left to show.
  useEffect(() => {
    if (gridMode && gridWorkspaces.length === 0) setGridMode(false);
  }, [gridMode, gridWorkspaces.length]);

  useEffect(() => {
    if (!selected) setShowRegen(false);
  }, [selected]);

  const onCreate = useCallback(
    async (args: CreateWorkspaceArgs): Promise<void> => {
      // Time each step so a "stuck on Creating…" report shows whether the wait is
      // the CreateWorkspace RPC (host worktree/agent setup) or the post-create
      // refresh (ListWorkspaces) — recorded to desktop.log via the diag bridge.
      const t0 = Date.now();
      diag('onCreate start', { repoPath: args.repoPath, shell: args.shell });
      const ws = await window.cs.createWorkspace(args);
      diag('onCreate createWorkspace done', { id: ws.id, ms: Date.now() - t0 });
      const tRefresh = Date.now();
      await refresh();
      diag('onCreate refresh done', { ms: Date.now() - tRefresh, totalMs: Date.now() - t0 });
      setSelectedId(ws.id);
    },
    [refresh],
  );

  // Agent-mode "new chat": create a rich Copilot workspace for the picked repo.
  // Rich chats are Copilot-only -- the daemon's richBackend() only marks a
  // workspace kind:'rich' when the agent is Copilot -- so force program:'copilot'
  // alongside rich:true. onCreate handles the post-create refresh + auto-select.
  const onCreateChat = useCallback(
    async (repoPath: string): Promise<void> => {
      await onCreate({ repoPath, program: 'copilot', rich: true });
    },
    [onCreate],
  );

  const onArchive = useCallback(
    (id: string): void => {
      // Show the confirmation modal instead of immediately archiving. Look the
      // workspace up from the ref (latest list) rather than closing over
      // `workspaces` so this handler keeps a stable identity, which lets
      // WorkspaceRow skip re-rendering when an unrelated workspace updates.
      // Mirrors the 'D' keyboard shortcut, which also reads workspacesRef.
      const workspace = workspacesRef.current.find((w) => w.id === id);
      if (workspace) {
        setWorkspaceToRemove(workspace);
      }
    },
    [],
  );

  const onSettings = useCallback(
    (id: string): void => {
      // Stable identity (reads the latest list from the ref) so WorkspaceRow
      // memoization stays effective; behaviour matches the previous inline
      // closure for any visible row.
      const ws = workspacesRef.current.find((w) => w.id === id);
      if (ws) setWorkspaceToEdit(ws);
    },
    [],
  );

  const onCycleMode = useCallback((): void => {
    setSidebarMode((cur) => {
      const idx = SIDEBAR_MODES.indexOf(cur);
      const next = SIDEBAR_MODES[(idx + 1) % SIDEBAR_MODES.length];
      localStorage.setItem(SIDEBAR_MODE_KEY, next);
      return next;
    });
  }, []);

  const toggleGridMember = useCallback((id: string): void => {
    setGridSelectedIds((prev) => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  }, []);

  const clearGridSelection = useCallback((): void => setGridSelectedIds(new Set()), []);

  const onGridColumnsChange = useCallback((cols: number): void => {
    setGridColumns(cols);
    if (cols > 0) localStorage.setItem(GRID_COLUMNS_KEY, String(cols));
    else localStorage.removeItem(GRID_COLUMNS_KEY);
  }, []);

  const onGridReorder = useCallback((orderedIds: string[]): void => {
    setGridOrder(orderedIds);
    localStorage.setItem(GRID_ORDER_KEY, JSON.stringify(orderedIds));
  }, []);

  const onGridRowHeightsChange = useCallback((heights: number[]): void => {
    setGridRowHeights(heights);
    localStorage.setItem(GRID_ROW_HEIGHTS_KEY, JSON.stringify(heights));
  }, []);

  const onConfirmRemove = useCallback(
    async (deleteWorktree: boolean): Promise<void> => {
      if (!workspaceToRemove) return;
      const id = workspaceToRemove.id;
      await window.cs.archiveWorkspace(id, { deleteWorktree });
      void window.cs.closeShell(id);
      setSelectedId((cur) => (cur === id ? null : cur));
      setWorkspaceToRemove(null);
      await refresh();
    },
    [workspaceToRemove, refresh],
  );

  const toggleAutoYes = useCallback(
    async (enabled: boolean): Promise<void> => {
      if (!selected) return;
      await window.cs.setWorkspaceAutoYes(selected.id, enabled);
      await refresh();
    },
    [selected, refresh],
  );

  // Keep the right panel within bounds as the window resizes.
  useEffect(() => {
    const clamp = (): void => setSideWidth((w) => Math.min(Math.max(w, SIDE_MIN), sideMax()));
    clamp();
    window.addEventListener('resize', clamp);
    return () => window.removeEventListener('resize', clamp);
  }, []);

  // Drag the gutter between the center pane and the right panel to resize it.
  const onGutterDown = useCallback((e: React.MouseEvent): void => {
    e.preventDefault();
    let last = 0;
    document.body.classList.add('is-col-resizing');
    const onMove = (ev: MouseEvent): void => {
      last = Math.min(sideMax(), Math.max(SIDE_MIN, window.innerWidth - ev.clientX));
      setSideWidth(last);
    };
    const onUp = (): void => {
      document.body.classList.remove('is-col-resizing');
      window.removeEventListener('mousemove', onMove);
      window.removeEventListener('mouseup', onUp);
      if (last > 0) localStorage.setItem(SIDE_KEY, String(Math.round(last)));
    };
    window.addEventListener('mousemove', onMove);
    window.addEventListener('mouseup', onUp);
  }, []);

  // Double-click the gutter to reset to the default width.
  const resetSide = useCallback((): void => {
    const next = Math.min(SIDE_DEFAULT, sideMax());
    setSideWidth(next);
    localStorage.setItem(SIDE_KEY, String(Math.round(next)));
  }, []);

  return (
    <div className="app-shell">
      <header className="top-bar">
        <div className="breadcrumb">
          {selected ? (
            <>
              <BreadcrumbCopy
                label={selected.title}
                path={selected.repoPath}
                tipAriaLabel="Copy repo path"
              />
              <span className="breadcrumb__sep">▸</span>
              <BreadcrumbCopy
                label={selected.branch}
                path={selected.worktreePath}
                className="breadcrumb__branch"
                tipAriaLabel="Copy workspace path"
              />
            </>
          ) : (
            <span>Workspaces</span>
          )}
        </div>
        <div className={`connection connection--${connection}`}>
          <span className="connection__dot" />
          {statusText}
        </div>
        <button
          type="button"
          className={`top-bar__mode${appMode === 'agent' ? ' is-active' : ''}`}
          title={
            appMode === 'agent'
              ? 'Switch to the standard workspace view'
              : 'Switch to the Copilot Agent view'
          }
          aria-label="Toggle app mode"
          aria-pressed={appMode === 'agent'}
          onClick={() => {
            const next: AppMode = appMode === 'agent' ? 'standard' : 'agent';
            localStorage.setItem(APP_MODE_KEY, next);
            setAppMode(next);
          }}
        >
          {appMode === 'agent' ? '⌂ Standard' : '💬 Agent'}
        </button>
        <button
          type="button"
          className={`top-bar__grid${gridMode ? ' is-active' : ''}`}
          title={gridMode ? 'Close agent grid' : 'Open a grid of the selected agents (select 2+)'}
          aria-label="Toggle agent grid"
          aria-pressed={gridMode}
          disabled={!gridMode && gridSelectedIds.size < 2}
          onClick={() => setGridMode((g) => !g)}
        >
          ▦ Grid{!gridMode && gridSelectedIds.size >= 2 ? ` (${gridSelectedIds.size})` : ''}
        </button>
        <button
          type="button"
          className="top-bar__help"
          title="Keyboard shortcuts (?)"
          aria-label="Keyboard shortcuts"
          onClick={() => setShowHelp(true)}
        >
          ?
        </button>
        <button
          type="button"
          className="top-bar__settings"
          title="Settings (Ctrl+,)"
          onClick={() => setShowSettings(true)}
        >
          ⚙
        </button>
      </header>

      {appMode === 'agent' ? (
        <AgentMode
          workspaces={workspaces}
          selectedId={selectedId}
          onSelectChat={setSelectedId}
          onCreateChat={onCreateChat}
        />
      ) : (
      <main
        className="workspace"
        style={{
          gridTemplateColumns: gridMode
            ? `${SIDEBAR_W}px minmax(0, 1fr)`
            : `${SIDEBAR_W}px minmax(${CENTER_MIN}px, 1fr) ${GUTTER_W}px ${sideWidth}px`,
        }}
      >
        <Sidebar
          workspaces={displayedWorkspaces}
          selectedId={selectedId}
          onSelect={setSelectedId}
          onArchive={onArchive}
          onSettings={onSettings}
          onNewWorkspace={() => {
            setCreateRepoPath(undefined);
            setShowCreate(true);
          }}
          onNewAtRepo={(repoPath) => {
            setCreateRepoPath(repoPath);
            setShowCreate(true);
          }}
          onCycleMode={onCycleMode}
          sidebarMode={sidebarMode}
          filter={sidebarFilter}
          onFilterChange={setSidebarFilter}
          statusFilter={statusFilter}
          counts={statusCounts}
          onStatusFilterChange={(v) => {
            localStorage.setItem(STATUS_FILTER_KEY, v);
            setStatusFilter(v);
          }}
          searchInputRef={searchInputRef}
          gridSelectedIds={gridSelectedIds}
          onToggleGridMember={toggleGridMember}
          onClearGridSelection={clearGridSelection}
        />
        {gridMode ? (
          <GridPane
            workspaces={gridWorkspaces}
            columns={gridColumns}
            onColumnsChange={onGridColumnsChange}
            onReorder={onGridReorder}
            rowHeights={gridRowHeights}
            onRowHeightsChange={onGridRowHeightsChange}
            onLeave={() => setGridMode(false)}
          />
        ) : (
          <>
            <CenterPane
              workspace={selected}
              onToggleAutoYes={toggleAutoYes}
              onRegenerate={() => setShowRegen(true)}
              regenerating={selectedRegenerating}
              regenPhase={selected?.regenPhase}
              onKillNow={() => {
                if (selected) void window.cs.forceRegenerate(selected.id);
              }}
            />
            <div
              className="col-gutter"
              role="separator"
              aria-orientation="vertical"
              aria-label="Resize right panel"
              title="Drag to resize · double-click to reset"
              onMouseDown={onGutterDown}
              onDoubleClick={resetSide}
            />
            <RightPanel workspace={selected} />
          </>
        )}
      </main>
      )}

      <footer className="status-bar">
        <span>Protocol v{hostVersion ?? '?'}</span>
        <span>
          {workspaces.length} workspace{workspaces.length === 1 ? '' : 's'} · thin client · Windows
          ConPTY
        </span>
      </footer>

      {showCreate && (
        <CreateWorkspaceModal
          onClose={() => {
            setShowCreate(false);
            setCreateRepoPath(undefined);
          }}
          onCreate={onCreate}
          initialRepoPath={createRepoPath}
        />
      )}

      {showSettings && (
        <SettingsModal
          onClose={() => setShowSettings(false)}
          onSaved={(s) => setRefreshMs(s.uiRefreshMs)}
        />
      )}

      {showRegen && selected && (
        <RegenerateModal
          workspace={selected}
          onConfirm={(handoff) => {
            const id = selected.id;
            setOptimisticRegenId(id);
            // The banner is driven by the polled `regenerating` flag, but a fast
            // (no-handoff) restart can finish between 2s polls, so show it
            // optimistically right away and let the poll take over / clear it.
            window.setTimeout(
              () => setOptimisticRegenId((cur) => (cur === id ? null : cur)),
              2500,
            );
            void window.cs.regenerateAgent(id, handoff);
          }}
          onClose={() => setShowRegen(false)}
        />
      )}

      {workspaceToRemove && (
        <RemoveWorkspaceModal
          workspaceTitle={workspaceToRemove.title}
          hasUncommittedChanges={workspaceToRemove.added > 0 || workspaceToRemove.removed > 0}
          hasWorktree={workspaceToRemove.hasWorktree}
          onConfirm={onConfirmRemove}
          onClose={() => setWorkspaceToRemove(null)}
        />
      )}

      {workspaceToEdit && (
        <WorkspaceSettingsModal
          workspace={workspaceToEdit}
          onClose={() => setWorkspaceToEdit(null)}
          onSaved={() => {
            setWorkspaceToEdit(null);
            void refresh();
          }}
        />
      )}

      {showBrowser && (
        <SessionBrowserModal
          onClose={() => setShowBrowser(false)}
          onResume={async (session) => {
            const repoPath = session.originRoot || undefined;
            // First attempt without confirmation. The host returns needsConfirm
            // (with the resolved absolute path) when the resume targets a repo
            // other than its own working directory — server-enforced, so the
            // client cannot skip it (F-03).
            let res = await window.cs.resumeCopilotSession(session.id, {
              title: session.name,
              repoPath,
              confirmed: false,
            });
            if (res.needsConfirm) {
              const ok = window.confirm(
                `Resume in "${res.absPath}"?\nA new worktree/branch will be created there.`,
              );
              if (!ok) return;
              res = await window.cs.resumeCopilotSession(session.id, {
                title: session.name,
                repoPath,
                confirmed: true,
              });
            }
            await refresh();
            if (res.workspace) setSelectedId(res.workspace.id);
          }}
        />
      )}

      {showWelcome && <WelcomeModal onClose={() => setShowWelcome(false)} />}

      {showHelp && <HelpModal onClose={() => setShowHelp(false)} />}
    </div>
  );
}
