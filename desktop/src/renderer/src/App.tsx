import { useCallback, useEffect, useRef, useState } from 'react';
import { CenterPane } from './components/CenterPane';
import { Composer } from './components/Composer';
import { RightPanel } from './components/RightPanel';
import { Sidebar } from './components/Sidebar';
import { CreateWorkspaceModal } from './components/CreateWorkspaceModal';
import { SettingsModal } from './components/SettingsModal';
import { RegenerateModal } from './components/RegenerateModal';
import type { CreateWorkspaceArgs } from '../../preload';
import type { WorkspaceInfo } from '../../main/host-client';
import { PROTO_VERSION } from '../../shared/proto-version';

type ConnectionState = 'connecting' | 'connected' | 'error';

// Right-panel resize bounds (the panel width is user-draggable and persisted).
const SIDE_MIN = 320;
const SIDE_DEFAULT = 420;
const SIDE_KEY = 'cs.sideWidth';
const SIDEBAR_W = 240;
const CENTER_MIN = 360;
const GUTTER_W = 6;

// Largest the right panel may grow to for the current window, keeping the sidebar
// and a usable center pane visible.
function sideMax(): number {
  return Math.max(
    SIDE_MIN,
    Math.min(window.innerWidth * 0.7, window.innerWidth - SIDEBAR_W - CENTER_MIN - GUTTER_W),
  );
}

// Workspaces created without an explicit title get auto-named by the agent after
// the first message. We remember which ones are still pending (across reloads) so
// only the first message triggers the rename.
const PENDING_TITLE_KEY = 'cs.autoTitlePending';

function loadPendingTitles(): Set<string> {
  try {
    const arr = JSON.parse(localStorage.getItem(PENDING_TITLE_KEY) ?? '[]') as unknown;
    return new Set(Array.isArray(arr) ? (arr as string[]) : []);
  } catch {
    return new Set();
  }
}

function savePendingTitles(s: Set<string>): void {
  try {
    localStorage.setItem(PENDING_TITLE_KEY, JSON.stringify([...s]));
  } catch {
    /* ignore */
  }
}

export function App(): JSX.Element {
  const [connection, setConnection] = useState<ConnectionState>('connecting');
  const [hostVersion, setHostVersion] = useState<number | null>(null);
  const [statusText, setStatusText] = useState('connecting to session-host…');
  const [workspaces, setWorkspaces] = useState<WorkspaceInfo[]>([]);
  const [selectedId, setSelectedId] = useState<string | null>(null);
  const [showSettings, setShowSettings] = useState(false);
  const [showCreate, setShowCreate] = useState(false);
  const [showRegen, setShowRegen] = useState(false);
  const [optimisticRegenId, setOptimisticRegenId] = useState<string | null>(null);
  const [refreshMs, setRefreshMs] = useState(2000);
  const [sideWidth, setSideWidth] = useState<number>(() => {
    const saved = Number(localStorage.getItem(SIDE_KEY));
    return Number.isFinite(saved) && saved >= SIDE_MIN ? saved : SIDE_DEFAULT;
  });
  const ready = useRef(false);
  const workspacesRef = useRef<WorkspaceInfo[]>([]);
  const aliveRef = useRef<Map<string, boolean>>(new Map());
  const sessionNameRef = useRef<Map<string, string>>(new Map());
  const regeneratingRef = useRef<Map<string, boolean>>(new Map());
  const connectionRef = useRef<ConnectionState>('connecting');
  const pendingTitlesRef = useRef<Set<string>>(loadPendingTitles());

  const refresh = useCallback(async (): Promise<WorkspaceInfo[]> => {
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
      for (const w of list) {
        nextAlive.set(w.id, w.alive);
        nextSessionName.set(w.id, w.sessionName);
        nextRegenerating.set(w.id, w.regenerating);
      }
      aliveRef.current = nextAlive;
      sessionNameRef.current = nextSessionName;
      regeneratingRef.current = nextRegenerating;
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

  useEffect(() => {
    let active = true;
    void (async () => {
      try {
        const hello = await window.cs.call({ method: 'Hello', clientVersion: PROTO_VERSION });
        if (!active) return;
        const hv = hello.hostVersion ?? 0;
        setHostVersion(hv);
        if (hv < PROTO_VERSION) {
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

    return () => {
      active = false;
      unsubFocus();
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
    const onKey = (e: KeyboardEvent): void => {
      if (e.ctrlKey && e.key === ',') {
        e.preventDefault();
        setShowSettings((s) => !s);
        return;
      }
      if (e.ctrlKey && (e.key === 'n' || e.key === 'N')) {
        e.preventDefault();
        setShowCreate(true);
        return;
      }
      if (e.altKey && e.key >= '1' && e.key <= '9') {
        const w = workspacesRef.current[Number(e.key) - 1];
        if (w) {
          e.preventDefault();
          setSelectedId(w.id);
        }
      }
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, []);

  workspacesRef.current = workspaces;
  connectionRef.current = connection;
  const selected = workspaces.find((w) => w.id === selectedId) ?? null;
  const selectedRegenerating =
    (selected?.regenerating ?? false) ||
    (optimisticRegenId !== null && optimisticRegenId === selected?.id);

  useEffect(() => {
    if (!selected) setShowRegen(false);
  }, [selected]);

  const onCreate = useCallback(
    async (args: CreateWorkspaceArgs): Promise<void> => {
      const ws = await window.cs.createWorkspace(args);
      // No explicit title → let the agent name it from the first message.
      if (!args.title || !args.title.trim()) {
        pendingTitlesRef.current.add(ws.id);
        savePendingTitles(pendingTitlesRef.current);
      }
      await refresh();
      setSelectedId(ws.id);
    },
    [refresh],
  );

  const onArchive = useCallback(
    async (id: string): Promise<void> => {
      await window.cs.archiveWorkspace(id);
      void window.cs.closeShell(id);
      if (pendingTitlesRef.current.delete(id)) savePendingTitles(pendingTitlesRef.current);
      setSelectedId((cur) => (cur === id ? null : cur));
      await refresh();
    },
    [refresh],
  );

  const toggleAutoYes = useCallback(
    async (enabled: boolean): Promise<void> => {
      if (!selected) return;
      await window.cs.setWorkspaceAutoYes(selected.id, enabled);
      await refresh();
    },
    [selected, refresh],
  );

  const sendInput = useCallback(
    (data: string) => {
      if (!selected) return;
      window.cs.sendInput(selected.sessionName, data);
      // First message for an untitled workspace → ask the agent to name it.
      if (pendingTitlesRef.current.has(selected.id)) {
        const message = data.replace(/[\r\n]+$/, '').trim();
        if (message) {
          pendingTitlesRef.current.delete(selected.id);
          savePendingTitles(pendingTitlesRef.current);
          void window.cs.generateWorkspaceTitle(selected.id, message);
        }
      }
    },
    [selected],
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
        <div className="brand">claude-squad</div>
        <div className="breadcrumb">
          {selected ? (
            <>
              <span>{selected.title}</span>
              <span className="breadcrumb__sep">▸</span>
              <span className="breadcrumb__branch">{selected.branch}</span>
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
          className="top-bar__settings"
          title="Settings (Ctrl+,)"
          onClick={() => setShowSettings(true)}
        >
          ⚙
        </button>
      </header>

      <main
        className="workspace"
        style={{
          gridTemplateColumns: `${SIDEBAR_W}px minmax(${CENTER_MIN}px, 1fr) ${GUTTER_W}px ${sideWidth}px`,
        }}
      >
        <Sidebar
          workspaces={workspaces}
          selectedId={selectedId}
          onSelect={setSelectedId}
          onArchive={onArchive}
          onNewWorkspace={() => setShowCreate(true)}
        />
        <CenterPane
          workspace={selected}
          onToggleAutoYes={toggleAutoYes}
          onRegenerate={() => setShowRegen(true)}
          regenerating={selectedRegenerating}
          regenPhase={selected?.regenPhase}
          onKillNow={() => {
            if (selected) void window.cs.forceRegenerate(selected.id);
          }}
          composer={<Composer disabled={!selected || selectedRegenerating} onSend={sendInput} />}
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
      </main>

      <footer className="status-bar">
        <span>Protocol v{hostVersion ?? '?'}</span>
        <span>
          {workspaces.length} workspace{workspaces.length === 1 ? '' : 's'} · thin client · Windows
          ConPTY
        </span>
      </footer>

      {showCreate && (
        <CreateWorkspaceModal onClose={() => setShowCreate(false)} onCreate={onCreate} />
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
    </div>
  );
}
