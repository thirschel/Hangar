import { useEffect, useMemo, useState } from 'react';
import { Diff, Hunk, parseDiff } from 'react-diff-view';
import 'react-diff-view/style/index.css';
import type { FileDiffInfo, WorkspaceInfo } from '../../../main/host-client';
import type { FileData } from 'react-diff-view';

// Diffs larger than this are not parsed/rendered inline. react-diff-view would
// build thousands of hunk rows synchronously on the main thread and freeze the
// UI; ~1.5 MB is far larger than any practically reviewable single-file diff.
export const MAX_DIFF_PREVIEW_BYTES = 1_500_000;

type ReviewPanelProps = {
  workspace: WorkspaceInfo | null;
  embedded?: boolean;
  onFilesCount?: (n: number) => void;
  // Whether the Changes tab is currently visible. When false the panel keeps its
  // last-known data (and the change-count badge) but pauses the 2.5s poll.
  active?: boolean;
};

export function ReviewPanel({
  workspace,
  embedded,
  onFilesCount,
  active = true,
}: ReviewPanelProps): JSX.Element {
  const [files, setFiles] = useState<FileDiffInfo[]>([]);
  const [selectedFile, setSelectedFile] = useState<string | null>(null);
  const [diff, setDiff] = useState('');
  const id = workspace?.id ?? null;

  // Reset the selection and changed-files list whenever the workspace changes so
  // a freshly selected workspace never briefly shows the previous one's data.
  useEffect(() => {
    setSelectedFile(null);
    setDiff('');
    setFiles([]);
    onFilesCount?.(0);
  }, [id, onFilesCount]);

  // Load the changed-files list, which also drives the change-count badge. We
  // always do one fetch (so the badge is correct even while the tab is hidden),
  // but only keep the 2.5s polling interval alive while the Changes tab is the
  // visible one (`active`). When the tab is hidden the interval is torn down;
  // re-showing it re-runs this effect and resumes polling.
  useEffect(() => {
    if (!id) return;
    let cancelled = false;
    const load = async (): Promise<void> => {
      try {
        const f = await window.cs.workspaceFiles(id);
        if (!cancelled) {
          setFiles(f);
          onFilesCount?.(f.length);
        }
      } catch {
        // transient; will retry on the next tick
      }
    };
    void load();
    if (!active) {
      return () => {
        cancelled = true;
      };
    }
    const timer = setInterval(() => void load(), 2500);
    return () => {
      cancelled = true;
      clearInterval(timer);
    };
  }, [id, active, onFilesCount]);

  useEffect(() => {
    if (!id || !selectedFile) {
      setDiff('');
      return;
    }
    let cancelled = false;
    window.cs
      .workspaceFileDiff(id, selectedFile)
      .then((d) => {
        if (!cancelled) setDiff(d);
      })
      .catch(() => {});
    return () => {
      cancelled = true;
    };
  }, [id, selectedFile]);

  if (!workspace) {
    return (
      <aside className="review-panel">
        {!embedded && <div className="panel-header">Changes</div>}
        <div className="empty-state">
          <div className="empty-state__title">No workspace selected</div>
          <p>Pick a workspace to review its changes.</p>
        </div>
      </aside>
    );
  }

  return (
    <aside className="review-panel">
      {!embedded && (
        <div className="panel-header">
          Changes <span className="count">{files.length}</span>
        </div>
      )}
      <div className="changed-files">
        {files.length === 0 && (
          <div className="empty-state">
            <p>No changes yet on this branch.</p>
          </div>
        )}
        {files.map((f) => (
          <div
            key={f.path}
            className={`file-row${f.path === selectedFile ? ' file-row--selected' : ''}`}
            onClick={() => setSelectedFile(f.path)}
            role="button"
            tabIndex={0}
          >
            <span className="file-row__path">{f.path}</span>
            <span className="diffstat">
              <span className="add">+{f.added}</span> <span className="del">-{f.removed}</span>
            </span>
          </div>
        ))}
      </div>
      {selectedFile && <DiffView text={diff} />}
    </aside>
  );
}

function DiffView({ text }: { text: string }): JSX.Element {
  const tooLarge = text.length > MAX_DIFF_PREVIEW_BYTES;
  const files = useMemo<FileData[]>(() => {
    if (tooLarge || !text.trim()) return [];
    return parseDiff(text);
  }, [text, tooLarge]);

  if (tooLarge) {
    const kb = Math.round(text.length / 1024);
    return (
      <div className="diff-view diff-view--empty">
        <div className="empty-state">
          <p>Diff too large to preview ({kb} KB).</p>
        </div>
      </div>
    );
  }

  if (!text.trim()) {
    return (
      <div className="diff-view diff-view--empty">
        <div className="empty-state">
          <p>No diff content available for this file.</p>
        </div>
      </div>
    );
  }

  return (
    <div className="diff-view">
      {files.length === 0 && (
        <div className="empty-state">
          <p>No parseable diff hunks for this file.</p>
        </div>
      )}
      {files.map((file, fileIndex) => (
        <section className="diff-file" key={`${file.oldPath}-${file.newPath}-${fileIndex}`}>
          <div className="diff-file__header">
            <span>{file.newPath || file.oldPath}</span>
            {file.type !== 'modify' && <span className="diff-file__type">{file.type}</span>}
          </div>
          {file.isBinary || file.hunks.length === 0 ? (
            <div className="diff-file__notice">No textual hunks to display.</div>
          ) : (
            <Diff viewType="unified" diffType={file.type} hunks={file.hunks} gutterType="default">
              {(hunks) =>
                hunks.map((hunk, hunkIndex) => (
                  <Hunk key={`${hunk.oldStart}-${hunk.newStart}-${hunkIndex}`} hunk={hunk} />
                ))
              }
            </Diff>
          )}
        </section>
      ))}
    </div>
  );
}
