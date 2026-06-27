import type { JSX } from 'react';
import { useEffect, useRef, useState, type ReactNode } from 'react';
import type { WorkspaceInfo } from '../../../main/host-client';
import { TermView } from './TermView';
import { effectiveColumns } from './grid-columns';
import { reorder } from './grid-reorder';
import { MIN_ROW_HEIGHT, normalizeRowHeights, withRowHeight } from './grid-rows';
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
  // Per-row heights (CSS px, indexed by row); missing rows default to
  // MIN_ROW_HEIGHT. Rows never shrink below MIN_ROW_HEIGHT.
  rowHeights?: number[];
  // Commit new per-row heights after a drag-resize (for persistence).
  onRowHeightsChange?: (heights: number[]) => void;
  // Renders a tile's live body for a workspace. Defaults to a TermView bound to
  // the workspace's terminal session; the rich agent view passes a ChatViewHost
  // so the same grid can tile rich Copilot chats.
  renderTile?: (w: WorkspaceInfo) => ReactNode;
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
  rowHeights,
  onRowHeightsChange,
  renderTile,
}: GridPaneProps): JSX.Element {
  const gridRef = useRef<HTMLDivElement>(null);
  const [width, setWidth] = useState(0);
  // Which tile's terminal currently has focus (drives the focus ring). Null = none.
  const [focusedId, setFocusedId] = useState<string | null>(null);
  // Drag-and-drop reorder state: the tile being dragged and the current drop target.
  const [dragId, setDragId] = useState<string | null>(null);
  const [dragOverId, setDragOverId] = useState<string | null>(null);
  // Live per-row heights during an active resize drag (null = use the prop).
  const [liveHeights, setLiveHeights] = useState<number[] | null>(null);
  // Cleanup function for an active row-resize drag. Stored so that if GridPane
  // unmounts mid-drag, the window listeners and userSelect override are torn down
  // instead of leaking for the lifetime of the page.
  const rowResizeCleanupRef = useRef<(() => void) | null>(null);

  // On unmount, run any in-flight row-resize cleanup (removes window listeners,
  // restores userSelect). Without this, an unmount during a drag leaves two
  // permanent window event listeners AND a stuck `userSelect: none` app-wide.
  useEffect(() => () => { rowResizeCleanupRef.current?.(); }, []);

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
    let debounceTimer: ReturnType<typeof setTimeout> | undefined;
    if (typeof ResizeObserver !== 'undefined') {
      // Debounce the ResizeObserver callback (~50ms) so rapid container
      // resize events (e.g. window drag) don't re-render N tiles per frame.
      observer = new ResizeObserver(() => {
        clearTimeout(debounceTimer);
        debounceTimer = setTimeout(update, 50);
      });
      observer.observe(el);
    }
    window.addEventListener('resize', update);
    return () => {
      clearTimeout(debounceTimer);
      observer?.disconnect();
      window.removeEventListener('resize', update);
    };
  }, []);

  const n = workspaces.length;
  const cols = Math.max(1, effectiveColumns(columns, width, n));
  const rowCount = Math.ceil(n / cols);
  const effectiveHeights = liveHeights ?? normalizeRowHeights(rowHeights ?? [], rowCount);

  // Begin a per-row resize drag from a tile's bottom handle. Tiles stretch to
  // their grid row track, so resizing one tile resizes its whole row. Updates
  // are live (liveHeights) and committed (onRowHeightsChange) on mouse-up.
  const startRowResize = (rowIndex: number, startY: number): void => {
    const base = normalizeRowHeights(rowHeights ?? [], rowCount);
    const startHeight = base[rowIndex] ?? MIN_ROW_HEIGHT;
    const prevUserSelect = document.body.style.userSelect;
    document.body.style.userSelect = 'none';

    const compute = (clientY: number): number[] =>
      withRowHeight(base, rowCount, rowIndex, startHeight + (clientY - startY));

    // rAF-throttle onMove so rapid mousemove events (one per vsync) don't
    // setState and re-render N tiles on every pixel of drag.
    let lastClientY = startY;
    let rafId: number | null = null;
    const onMove = (ev: MouseEvent): void => {
      lastClientY = ev.clientY;
      if (rafId !== null) return;
      rafId = requestAnimationFrame(() => {
        rafId = null;
        setLiveHeights(compute(lastClientY));
      });
    };

    // Centralised teardown: remove both listeners, cancel any pending rAF, and
    // restore the body's userSelect. Stored in rowResizeCleanupRef so that an
    // unmount mid-drag (which never reaches onUp) can still run it.
    const cleanup = (): void => {
      window.removeEventListener('mousemove', onMove);
      window.removeEventListener('mouseup', onUp);
      if (rafId !== null) {
        cancelAnimationFrame(rafId);
        rafId = null;
      }
      document.body.style.userSelect = prevUserSelect;
      rowResizeCleanupRef.current = null;
    };

    const onUp = (ev: MouseEvent): void => {
      cleanup();
      const finalHeights = compute(ev.clientY);
      setLiveHeights(null);
      onRowHeightsChange?.(finalHeights);
    };
    window.addEventListener('mousemove', onMove);
    window.addEventListener('mouseup', onUp);
    rowResizeCleanupRef.current = cleanup;
  };

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
        style={{
          gridTemplateColumns: `repeat(${cols}, minmax(0, 1fr))`,
          gridTemplateRows: effectiveHeights.map((h) => `${h}px`).join(' '),
        }}
      >
        {workspaces.map((w, i) => {
          const status = workspaceStatus(w);
          const focused = w.id === focusedId;
          const rowIndex = Math.floor(i / cols);
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
                {renderTile ? (
                  renderTile(w)
                ) : (
                  <TermView key={w.sessionName} sessionName={w.sessionName} />
                )}
              </div>
              <div
                className="grid-tile__resize"
                title="Drag to resize row height"
                aria-hidden="true"
                onMouseDown={(e) => {
                  e.preventDefault();
                  e.stopPropagation();
                  startRowResize(rowIndex, e.clientY);
                }}
              />
            </div>
          );
        })}
      </div>
    </section>
  );
}
