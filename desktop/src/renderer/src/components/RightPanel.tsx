import { useEffect, useState } from 'react';
import type { WorkspaceInfo } from '../../../main/host-client';
import { ReviewPanel } from './ReviewPanel';
import { FilesPanel } from './FilesPanel';

type TopTab = 'changes' | 'files';

type RightPanelProps = {
  workspace: WorkspaceInfo | null;
};

// RightPanel is the VS Code / Conductor-style side column: a region tabbed between
// the worktree file browser ("All files") and the git review ("Changes"). The shell
// terminal now lives in the center pane (see CenterTerminal) so it gets more width.
export function RightPanel({ workspace }: RightPanelProps): JSX.Element {
  const [topTab, setTopTab] = useState<TopTab>('changes');
  const [filesVisited, setFilesVisited] = useState(false);
  const [changeCount, setChangeCount] = useState(0);

  const wsId = workspace?.id ?? null;

  // Reset views when the workspace changes.
  useEffect(() => {
    setTopTab('changes');
    setFilesVisited(false);
  }, [wsId]);

  const selectTop = (t: TopTab): void => {
    setTopTab(t);
    if (t === 'files') setFilesVisited(true);
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
            <ReviewPanel
              workspace={workspace}
              embedded
              onFilesCount={setChangeCount}
              active={topTab === 'changes'}
            />
          </div>
          <div className="tab-pane" hidden={topTab !== 'files'}>
            {filesVisited && <FilesPanel workspace={workspace} embedded />}
          </div>
        </div>
      </div>
    </aside>
  );
}
