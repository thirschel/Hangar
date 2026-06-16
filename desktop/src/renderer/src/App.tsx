import { useCallback, useEffect, useRef, useState } from 'react';
import { AgentTerminal } from './components/AgentTerminal';
import { CenterPane } from './components/CenterPane';
import { Composer } from './components/Composer';
import { ReviewPanel } from './components/ReviewPanel';
import { RunPanel } from './components/RunPanel';
import { Sidebar } from './components/Sidebar';
import type { CreateWorkspaceArgs } from '../../preload';
import type { WorkspaceInfo } from '../../main/host-client';

type ConnectionState = 'connecting' | 'connected' | 'error';

export function App(): JSX.Element {
  const [connection, setConnection] = useState<ConnectionState>('connecting');
  const [hostVersion, setHostVersion] = useState<number | null>(null);
  const [statusText, setStatusText] = useState('connecting to session-host…');
  const [workspaces, setWorkspaces] = useState<WorkspaceInfo[]>([]);
  const [selectedId, setSelectedId] = useState<string | null>(null);
  const ready = useRef(false);

  const refresh = useCallback(async (): Promise<WorkspaceInfo[]> => {
    try {
      const list = await window.cs.listWorkspaces();
      setWorkspaces(list);
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

    const timer = setInterval(() => {
      if (ready.current) void refresh();
    }, 2000);
    return () => {
      active = false;
      clearInterval(timer);
    };
  }, [refresh]);

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
      </header>

      <main className="workspace">
        <Sidebar
          workspaces={workspaces}
          selectedId={selectedId}
          onSelect={setSelectedId}
          onCreate={onCreate}
          onArchive={onArchive}
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
    </div>
  );
}
