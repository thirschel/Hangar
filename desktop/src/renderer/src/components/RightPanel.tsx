import { useEffect, useRef, useState } from 'react';
import type { WorkspaceInfo } from '../../../main/host-client';
import { ReviewPanel } from './ReviewPanel';
import { FilesPanel } from './FilesPanel';
import { ShellTerminal } from './ShellTerminal';
import type { TermViewHandle } from './TermView';

type TopTab = 'changes' | 'files';

type RightPanelProps = {
  workspace: WorkspaceInfo | null;
};

// RightPanel is the VS Code / Conductor-style side column: a top region tabbed
// between the worktree file browser ("All files") and the git review ("Changes"),
// over a collapsible, on-demand Terminal. The agent terminal lives in the center
// pane, so everything here is visible alongside it.
export function RightPanel({ workspace }: RightPanelProps): JSX.Element {
  const [topTab, setTopTab] = useState<TopTab>('changes');
  const [filesVisited, setFilesVisited] = useState(false);
  const [changeCount, setChangeCount] = useState(0);
  // The terminal is collapsed by default and nothing is spawned until the user
  // opens it. Once created, the shell instance persists across collapse/expand —
  // only the close (✕) button kills it.
  const [bottomOpen, setBottomOpen] = useState(false);
  const [terminalCreated, setTerminalCreated] = useState(false);
  const shellRef = useRef<TermViewHandle>(null);

  const wsId = workspace?.id ?? null;

  // Reset views + the terminal when the workspace changes (don't carry a terminal
  // across workspaces; the daemon keeps the shell alive for re-open).
  useEffect(() => {
    setTopTab('changes');
    setFilesVisited(false);
    setBottomOpen(false);
    setTerminalCreated(false);
  }, [wsId]);

  const refitSoon = (): void => {
    setTimeout(() => shellRef.current?.refit(), 0);
  };

  const selectTop = (t: TopTab): void => {
    setTopTab(t);
    if (t === 'files') setFilesVisited(true);
  };

  // Open (creating on first use) the terminal — slides the panel up.
  const openTerminal = (): void => {
    setTerminalCreated(true);
    setBottomOpen(true);
    refitSoon();
  };

  // The arrow only toggles visibility; the instance is kept alive while collapsed.
  const toggleBottom = (): void => {
    if (bottomOpen) setBottomOpen(false);
    else openTerminal();
  };

  // ✕ closes the instance for good and collapses back to the bar.
  const killTerminal = (): void => {
    if (wsId) void window.cs.closeShell(wsId);
    setTerminalCreated(false);
    setBottomOpen(false);
  };

  return (
    <aside className="side-panel">
      <div className="side-panel__top">
        <div className="mini-tabs" role="tablist" aria-label="Files and changes">
          <button
            className={`mini-tab${topTab === 'files' ? ' mini-tab--active' : ''}`}
            type="button"
            onClick={() => selectTop('files')}
            disabled={!workspace}
          >
            All files
          </button>
          <button
            className={`mini-tab${topTab === 'changes' ? ' mini-tab--active' : ''}`}
            type="button"
            onClick={() => selectTop('changes')}
          >
            Changes
            {workspace && changeCount > 0 && <span className="count">{changeCount}</span>}
          </button>
        </div>
        <div className="tab-content">
          <div className="tab-pane" hidden={topTab !== 'changes'}>
            <ReviewPanel workspace={workspace} embedded onFilesCount={setChangeCount} />
          </div>
          <div className="tab-pane" hidden={topTab !== 'files'}>
            {filesVisited && <FilesPanel workspace={workspace} embedded />}
          </div>
        </div>
      </div>

      <div className={`side-panel__bottom${bottomOpen ? ' side-panel__bottom--open' : ''}`}>
        <div className="mini-tabs" aria-label="Terminal">
          <button
            className="side-panel__collapse"
            type="button"
            title={bottomOpen ? 'Collapse terminal' : 'Open terminal'}
            aria-expanded={bottomOpen}
            onClick={toggleBottom}
            disabled={!workspace}
          >
            {bottomOpen ? '▾' : '▸'}
          </button>
          <button
            className={`mini-tab${bottomOpen ? ' mini-tab--active' : ''}`}
            type="button"
            onClick={openTerminal}
            disabled={!workspace}
          >
            Terminal
          </button>
          <div className="tab-bar__spacer" />
          {terminalCreated && (
            <button
              className="icon-button side-panel__kill"
              type="button"
              title="Close terminal"
              onClick={killTerminal}
            >
              ✕
            </button>
          )}
        </div>
        {terminalCreated && (
          <div className="side-panel__bottom-body">
            <ShellTerminal ref={shellRef} key={wsId ?? 'none'} workspace={workspace} />
          </div>
        )}
      </div>
    </aside>
  );
}
