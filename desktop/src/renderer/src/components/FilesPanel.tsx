import type { JSX } from 'react';
import { useEffect, useState } from 'react';
import type { DirEntry, FileContents, WorkspaceInfo } from '../../../main/host-client';

const joinRel = (dir: string, name: string): string => (dir ? `${dir}/${name}` : name);

type TreeNodeProps = {
  worktreePath: string;
  entry: DirEntry;
  rel: string;
  depth: number;
  selected: string | null;
  onOpen: (rel: string) => void;
};

function TreeNode({
  worktreePath,
  entry,
  rel,
  depth,
  selected,
  onOpen,
}: TreeNodeProps): JSX.Element {
  const [open, setOpen] = useState(false);
  const [children, setChildren] = useState<DirEntry[] | null>(null);

  useEffect(() => {
    if (open && children === null) {
      window.cs
        .listDir(worktreePath, rel)
        .then(setChildren)
        .catch(() => setChildren([]));
    }
  }, [open, children, worktreePath, rel]);

  const pad = { paddingLeft: `${depth * 14 + 8}px` };

  if (!entry.dir) {
    return (
      <div
        className={`tree-row tree-row--file${selected === rel ? ' tree-row--selected' : ''}`}
        style={pad}
        onClick={() => onOpen(rel)}
        role="button"
        tabIndex={0}
      >
        <span className="tree-icon">📄</span>
        {entry.name}
      </div>
    );
  }

  return (
    <>
      <div
        className="tree-row tree-row--dir"
        style={pad}
        onClick={() => setOpen((o) => !o)}
        role="button"
        tabIndex={0}
      >
        <span className="tree-icon">{open ? '▾' : '▸'}</span>
        {entry.name}
      </div>
      {open &&
        (children ?? []).map((c) => (
          <TreeNode
            key={c.name}
            worktreePath={worktreePath}
            entry={c}
            rel={joinRel(rel, c.name)}
            depth={depth + 1}
            selected={selected}
            onOpen={onOpen}
          />
        ))}
    </>
  );
}

type FilesPanelProps = {
  workspace: WorkspaceInfo | null;
  embedded?: boolean;
};

export function FilesPanel({ workspace, embedded }: FilesPanelProps): JSX.Element {
  const worktreePath = workspace?.worktreePath ?? '';
  const [roots, setRoots] = useState<DirEntry[]>([]);
  const [selected, setSelected] = useState<string | null>(null);
  const [contents, setContents] = useState<FileContents | null>(null);
  const [nonce, setNonce] = useState(0);

  useEffect(() => {
    setSelected(null);
    setContents(null);
    if (!worktreePath) {
      setRoots([]);
      return;
    }
    let active = true;
    window.cs
      .listDir(worktreePath, '')
      .then((r) => {
        if (active) setRoots(r);
      })
      .catch(() => {
        if (active) setRoots([]);
      });
    return () => {
      active = false;
    };
  }, [worktreePath, nonce]);

  const open = (rel: string): void => {
    setSelected(rel);
    setContents(null);
    window.cs
      .readFile(worktreePath, rel)
      .then(setContents)
      .catch((e) =>
        setContents({ kind: 'error', message: e instanceof Error ? e.message : String(e) }),
      );
  };

  if (!workspace) {
    return (
      <div className="files-panel files-panel--empty">Select a workspace to browse its files.</div>
    );
  }

  return (
    <div className="files-panel">
      <div className="files-tree">
        <div className="files-tree__header">
          <span>{embedded ? '' : 'Files'}</span>
          <button
            type="button"
            className="icon-button"
            title="Refresh"
            onClick={() => setNonce((n) => n + 1)}
          >
            ⟳
          </button>
        </div>
        <div className="files-tree__body">
          {roots.map((e) => (
            <TreeNode
              key={e.name}
              worktreePath={worktreePath}
              entry={e}
              rel={e.name}
              depth={0}
              selected={selected}
              onOpen={open}
            />
          ))}
        </div>
      </div>
      <div className="files-viewer">
        {selected ? (
          <>
            <div className="files-viewer__path">{selected}</div>
            <div className="files-viewer__body">
              {contents === null && <div className="files-viewer__note">Loading…</div>}
              {contents?.kind === 'text' && (
                <pre className="files-viewer__pre">{contents.text}</pre>
              )}
              {contents?.kind === 'binary' && (
                <div className="files-viewer__note">Binary file — not shown.</div>
              )}
              {contents?.kind === 'tooLarge' && (
                <div className="files-viewer__note">
                  File too large to preview ({contents.size.toLocaleString()} bytes).
                </div>
              )}
              {contents?.kind === 'error' && (
                <div className="files-viewer__note">Error: {contents.message}</div>
              )}
            </div>
          </>
        ) : (
          <div className="files-viewer__note">Select a file to view its contents.</div>
        )}
      </div>
    </div>
  );
}
