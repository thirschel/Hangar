import type { JSX } from 'react';
import { useEffect, useRef, useState } from 'react';
import type { WorkspaceInfo } from '../../../main/host-client';
import { TermView } from './TermView';
import { effectiveColumns } from './grid-columns';
import { reorder } from './grid-reorder';
import { workspaceStatus } from './workspace-status';

type GridPaneProps = {
  // The agents to tile, in display order (the multi-selected workspaces).
  workspaces: WorkspaceInfo[];
  // Raw "agents per row" setting; 0 means Auto (derive from width).
  columns: number;
  onColumnsChange: (cols: number) => void;
  onLeave: () => void;
  // Reorder tiles via drag-and-drop; receives the full new id order. When
  // omitted, tiles are not draggable.
  onReorder?: (orderedIds: string[]) => void;
};

// GridPane tiles several agents at once. Each tile is a self-contained, live,
// focusable TermView bound to that workspace's agent session, so clicking a tile
// focuses its terminal and typing goes straight to that agent (with local echo).
// It replaces the single-agent center/right panes while active; the sidebar
// stays for selection.
export function GridPane({
  workspaces,
  columns,
  onColumnsChange,
  onLeave,
  onReorder,
}: GridPaneProps): JSX.Element {
  const gridRef = useRef<HTMLDivElement>(null);
  const [width, setWidth] = useState(0);
  // Which tile's terminal currently has focus (drives the focus ring). Null = none.
  const [focusedId, setFocusedId] = useState<string | null>(null);
  // Drag-and-drop reorder state: the tile being dragged and the current drop target.
  const [dragId, setDragId] = useState<string | null>(null);
  const [dragOverId, setDragOverId] = useState<string | null>(null);

  // Drop the dragged tile onto targetId. Because tiles are keyed by id (and each
  // TermView by sessionName), reordering only moves the keyed nodes — the live
  // terminals are preserved, not remounted.
  const handleDrop = (targetId: string): void => {
    const sourceId = dragId;
    setDragId(null);
    setDragOverId(null);
    if (onReorder && sourceId && sourceId !== targetId) {
      onReorder(reorder(
        workspaces.map((w) => w.id),
        sourceId,
        targetId,
      ));
    }
  };

  // Track the grid container width so Auto can pick a column count. Uses
  // ResizeObserver when available (precise: catches container-only changes) and
  // always also listens to window resize as a fallback (covers environments
  // without ResizeObserver, e.g. jsdom tests).
  useEffect(() => {
    const el = gridRef.current;
    if (!el) return;
    const update = (): void => setWidth(el.clientWidth);
    update();
    let observer: ResizeObserver | undefined;
    if (typeof ResizeObserver !== 'undefined') {
      observer = new ResizeObserver(update);
      observer.observe(el);
    }
    window.addEventListener('resize', update);
    return () => {
      observer?.disconnect();
      window.removeEventListener('resize', update);
    };
  }, []);

  const n = workspaces.length;
  const cols = Math.max(1, effectiveColumns(columns, width, n));

  return (
    <section className="grid-pane" aria-label="Agent grid">
      <div className="grid-pane__bar">
        <span className="grid-pane__title">
          Grid · {n} agent{n === 1 ? '' : 's'}
        </span>
        <div className="grid-pane__spacer" />
        <label className="grid-pane__percol">
          Per row:
          <select
            className="grid-pane__cols-select"
            aria-label="Agents per row"
            value={Math.min(Math.max(columns, 0), n)}
            onChange={(e) => onColumnsChange(Number(e.target.value))}
          >
            <option value={0}>Auto</option>
            {Array.from({ length: n }, (_, i) => i + 1).map((c) => (
              <option key={c} value={c}>
                {c}
              </option>
            ))}
          </select>
        </label>
        <button
          type="button"
          className="grid-pane__close"
          title="Close grid (Esc)"
          aria-label="Close grid"
          onClick={onLeave}
        >
          ✕ Close grid
        </button>
      </div>

      <div
        className="grid-pane__grid"
        ref={gridRef}
        style={{ gridTemplateColumns: `repeat(${cols}, minmax(0, 1fr))` }}
      >
        {workspaces.map((w) => {
          const status = workspaceStatus(w);
          const focused = w.id === focusedId;
          const dragging = dragId === w.id;
          const dropTarget = !!dragId && dragOverId === w.id && dragId !== w.id;
          return (
            <div
              key={w.id}
              className={`grid-tile${focused ? ' grid-tile--focused' : ''}${
                dragging ? ' grid-tile--dragging' : ''
              }${dropTarget ? ' grid-tile--dragover' : ''}`}
              onMouseDown={() => setFocusedId(w.id)}
              onFocus={() => setFocusedId(w.id)}
              onDragOver={
                onReorder
                  ? (e) => {
                      e.preventDefault();
                      e.dataTransfer.dropEffect = 'move';
                      if (dragOverId !== w.id) setDragOverId(w.id);
                    }
                  : undefined
              }
              onDrop={
                onReorder
                  ? (e) => {
                      e.preventDefault();
                      handleDrop(w.id);
                    }
                  : undefined
              }
            >
              <div
                className="grid-tile__header"
                draggable={!!onReorder}
                onDragStart={
                  onReorder
                    ? (e) => {
                        setDragId(w.id);
                        e.dataTransfer.effectAllowed = 'move';
                        e.dataTransfer.setData('text/plain', w.id);
                      }
                    : undefined
                }
                onDragEnd={
                  onReorder
                    ? () => {
                        setDragId(null);
                        setDragOverId(null);
                      }
                    : undefined
                }
              >
                {onReorder && (
                  <span className="grid-tile__drag" title="Drag to reorder" aria-hidden="true">
                    ⠿
                  </span>
                )}
                <span
                  className={`grid-tile__dot is-${status}`}
                  title={status}
                  aria-hidden="true"
                />
                <span className="grid-tile__title" title={w.title}>
                  {w.title}
                </span>
                {w.autoYes && <span className="grid-tile__badge">AutoYes</span>}
                {w.regenerating && <span className="grid-tile__badge">Regenerating…</span>}
              </div>
              <div className="grid-tile__term">
                <TermView key={w.sessionName} sessionName={w.sessionName} />
              </div>
            </div>
          );
        })}
      </div>
    </section>
  );
}
