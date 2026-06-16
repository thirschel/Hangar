import { useEffect, useMemo, useState } from 'react';
import { Diff, Hunk, parseDiff } from 'react-diff-view';
import 'react-diff-view/style/index.css';
import type { FileDiffInfo, WorkspaceInfo } from '../../../main/host-client';
import type { FileData } from 'react-diff-view';

type ReviewPanelProps = {
  workspace: WorkspaceInfo | null;
};

export function ReviewPanel({ workspace }: ReviewPanelProps): JSX.Element {
  const [files, setFiles] = useState<FileDiffInfo[]>([]);
  const [selectedFile, setSelectedFile] = useState<string | null>(null);
  const [diff, setDiff] = useState('');
  const id = workspace?.id ?? null;

  useEffect(() => {
    setSelectedFile(null);
    setDiff('');
    setFiles([]);
    if (!id) return;
    let active = true;
    const load = async (): Promise<void> => {
      try {
        const f = await window.cs.workspaceFiles(id);
        if (active) setFiles(f);
      } catch {
        // transient; will retry on the next tick
      }
    };
    void load();
    const timer = setInterval(() => void load(), 2500);
    return () => {
      active = false;
      clearInterval(timer);
    };
  }, [id]);

  useEffect(() => {
    if (!id || !selectedFile) {
      setDiff('');
      return;
    }
    let active = true;
    window.cs
      .workspaceFileDiff(id, selectedFile)
      .then((d) => {
        if (active) setDiff(d);
      })
      .catch(() => {});
    return () => {
      active = false;
    };
  }, [id, selectedFile]);

  if (!workspace) {
    return (
      <aside className="review-panel">
        <div className="panel-header">Changes</div>
        <div className="empty-state">
          <div className="empty-state__title">No workspace selected</div>
          <p>Pick a workspace to review its changes.</p>
        </div>
      </aside>
    );
  }

  return (
    <aside className="review-panel">
      <div className="panel-header">
        Changes <span className="count">{files.length}</span>
      </div>
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
  const files = useMemo<FileData[]>(() => {
    if (!text.trim()) return [];
    return parseDiff(text);
  }, [text]);

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
