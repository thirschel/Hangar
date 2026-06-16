import { useCallback, useEffect, useRef, useState } from 'react';
import { AgentTerminal } from './components/AgentTerminal';
import { CenterPane } from './components/CenterPane';
import { Composer } from './components/Composer';
import { ReviewPanel } from './components/ReviewPanel';
import { RunPanel } from './components/RunPanel';
import { Sidebar } from './components/Sidebar';
import { SettingsModal } from './components/SettingsModal';
import type { CreateWorkspaceArgs } from '../../preload';
import type { WorkspaceInfo } from '../../main/host-client';

type ConnectionState = 'connecting' | 'connected' | 'error';

export function App(): JSX.Element {
  const [connection, setConnection] = useState<ConnectionState>('connecting');
  const [hostVersion, setHostVersion] = useState<number | null>(null);
  const [statusText, setStatusText] = useState('connecting to session-host…');
  const [workspaces, setWorkspaces] = useState<WorkspaceInfo[]>([]);
  const [selectedId, setSelectedId] = useState<string | null>(null);
  const [showSettings, setShowSettings] = useState(false);
  const [createNonce, setCreateNonce] = useState(0);
  const [refreshMs, setRefreshMs] = useState(2000);
  const ready = useRef(false);
  const workspacesRef = useRef<WorkspaceInfo[]>([]);
  const aliveRef = useRef<Map<string, boolean>>(new Map());
  const connectionRef = useRef<ConnectionState>('connecting');

  const refresh = useCallback(async (): Promise<WorkspaceInfo[]> => {
    try {
      const list = await window.cs.listWorkspaces();
      // Notify when an agent session goes alive -> not alive (i.e. it exited).
      const prev = aliveRef.current;
      if (prev.size > 0) {
        for (const w of list) {
          if (prev.get(w.id) === true && !w.alive) {
            void window.cs.notify({ title: 'Agent finished', body: w.title, workspaceId: w.id });
          }
        }
      }
      const next = new Map<string, boolean>();
      for (const w of list) next.set(w.id, w.alive);
      aliveRef.current = next;
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
        const hello = await window.cs.call({ method: 'Hello', clientVersion: 3 });
        if (!active) return;
        const hv = hello.hostVersion ?? 0;
        setHostVersion(hv);
        if (hv < 3) {
          setConnection('error');
          setStatusText(`daemon is v${hv} — the desktop app needs v3. Run \`cs reset\`, then relaunch.`);
          return;
        }
        setConnection('connected');
        setStatusText(`connected · daemon v${hv}`);
        ready.current = true;
        await refresh();
      } catch (error) {
        if (!active) return;
        setConnection('error');
        setStatusText(`cannot reach session-host · ${error instanceof Error ? error.message : String(error)}`);
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
      if (e.key === 'Escape') {
        setShowSettings(false);
        return;
      }
      if (e.ctrlKey && e.key === ',') {
        e.preventDefault();
        setShowSettings((s) => !s);
        return;
      }
      if (e.ctrlKey && (e.key === 'n' || e.key === 'N')) {
        e.preventDefault();
        setCreateNonce((n) => n + 1);
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

  const onCreate = useCallback(
    async (args: CreateWorkspaceArgs): Promise<void> => {
      const ws = await window.cs.createWorkspace(args);
      await refresh();
      setSelectedId(ws.id);
    },
    [refresh],
  );

  const onArchive = useCallback(
    async (id: string): Promise<void> => {
      await window.cs.archiveWorkspace(id);
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

  const sendInput = useCallback((data: string) => window.cs.sendInput(data), []);

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

      <main className="workspace">
        <Sidebar
          workspaces={workspaces}
          selectedId={selectedId}
          onSelect={setSelectedId}
          onCreate={onCreate}
          onArchive={onArchive}
          openCreateNonce={createNonce}
        />
        <CenterPane
          workspace={selected}
          onToggleAutoYes={toggleAutoYes}
          terminal={<AgentTerminal key={selected?.sessionName ?? 'none'} sessionName={selected?.sessionName ?? null} />}
          composer={<Composer disabled={!selected} onSend={sendInput} />}
        />
        <div className="right-column">
          <ReviewPanel workspace={selected} />
          <RunPanel workspace={selected} />
        </div>
      </main>

      <footer className="status-bar">
        <span>Protocol v{hostVersion ?? '?'}</span>
        <span>
          {workspaces.length} workspace{workspaces.length === 1 ? '' : 's'} · thin client · Windows ConPTY
        </span>
      </footer>

      {showSettings && (
        <SettingsModal onClose={() => setShowSettings(false)} onSaved={(s) => setRefreshMs(s.uiRefreshMs)} />
      )}
    </div>
  );
}
