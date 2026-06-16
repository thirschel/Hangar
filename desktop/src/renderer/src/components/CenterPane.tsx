import { useEffect, useRef, useState, type ReactNode } from 'react';
import type { WorkspaceInfo } from '../../../main/host-client';
import { TermView, type TermViewHandle } from './TermView';
import { FilesPanel } from './FilesPanel';
import { ShellTerminal } from './ShellTerminal';

type Tab = 'agent' | 'files' | 'terminal';

type CenterPaneProps = {
  workspace: WorkspaceInfo | null;
  onToggleAutoYes: (enabled: boolean) => void;
  composer: ReactNode;
};

export function CenterPane({ workspace, onToggleAutoYes, composer }: CenterPaneProps): JSX.Element {
  const [tab, setTab] = useState<Tab>('agent');
  // Lazily mount Files/Terminal only after first visit (per workspace), so we
  // don't spin up a shell for every workspace you merely select.
  const [visited, setVisited] = useState<{ files: boolean; terminal: boolean }>({ files: false, terminal: false });
  const agentRef = useRef<TermViewHandle>(null);
  const shellRef = useRef<TermViewHandle>(null);

  const wsId = workspace?.id ?? null;
  // Reset to the Agent tab and clear lazy-mount flags when the workspace changes.
  useEffect(() => {
    setTab('agent');
    setVisited({ files: false, terminal: false });
  }, [wsId]);

  const select = (next: Tab): void => {
    setTab(next);
    if (next === 'files') setVisited((v) => (v.files ? v : { ...v, files: true }));
    if (next === 'terminal') setVisited((v) => (v.terminal ? v : { ...v, terminal: true }));
    // A hidden xterm can't measure itself; refit once it's visible.
    setTimeout(() => {
      if (next === 'agent') agentRef.current?.refit();
      if (next === 'terminal') shellRef.current?.refit();
    }, 0);
  };

  return (
    <section className="center-pane">
      <div className="tab-bar" role="tablist" aria-label="Workspace views">
        <button className={`tab${tab === 'agent' ? ' tab--active' : ''}`} type="button" onClick={() => select('agent')}>
          Agent
        </button>
        <button
          className={`tab${tab === 'files' ? ' tab--active' : ''}`}
          type="button"
          onClick={() => select('files')}
          disabled={!workspace}
        >
          Files
        </button>
        <button
          className={`tab${tab === 'terminal' ? ' tab--active' : ''}`}
          type="button"
          onClick={() => select('terminal')}
          disabled={!workspace}
        >
          Terminal
        </button>
        <div className="tab-bar__spacer" />
        {workspace && tab === 'agent' && (
          <label className="autoyes" title="Auto-approve agent prompts (host-side)">
            <input type="checkbox" checked={workspace.autoYes} onChange={(e) => onToggleAutoYes(e.target.checked)} />
            AutoYes
          </label>
        )}
      </div>

      <div className="tab-content">
        <div className="tab-pane" hidden={tab !== 'agent'}>
          <div className="agent-surface">
            <TermView ref={agentRef} key={workspace?.sessionName ?? 'none'} sessionName={workspace?.sessionName ?? null} />
          </div>
          {composer}
        </div>

        <div className="tab-pane" hidden={tab !== 'files'}>
          {visited.files && <FilesPanel workspace={workspace} />}
        </div>

        <div className="tab-pane" hidden={tab !== 'terminal'}>
          {visited.terminal && <ShellTerminal ref={shellRef} key={workspace?.id ?? 'none'} workspace={workspace} />}
        </div>
      </div>
    </section>
  );
}
