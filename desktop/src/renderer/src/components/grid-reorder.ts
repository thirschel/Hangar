/**
 * Pure tile-reordering helper for the multi-agent grid's drag-and-drop.
 *
 * Dropping `draggedId` onto `targetId` moves the dragged tile to the target's
 * slot: dragging *forward* (dragged is before the target) lands it immediately
 * after the target; dragging *backward* lands it immediately before the target.
 * Every other id keeps its relative order. Returns a copy of the input order
 * unchanged when the ids are equal or either id is not present.
 */
export function reorder(ids: readonly string[], draggedId: string, targetId: string): string[] {
  const next = [...ids];
  if (draggedId === targetId) return next;
  const from = next.indexOf(draggedId);
  const to = next.indexOf(targetId);
  if (from === -1 || to === -1) return next;

  next.splice(from, 1);
  const targetIdx = next.indexOf(targetId);
  const insertIdx = from < to ? targetIdx + 1 : targetIdx;
  next.splice(insertIdx, 0, draggedId);
  return next;
}
