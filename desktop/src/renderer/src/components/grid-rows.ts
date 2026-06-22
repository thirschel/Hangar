/**
 * Pure row-height helpers for the multi-agent grid's per-row drag-resize.
 * Rows never shrink below MIN_ROW_HEIGHT; heights are whole CSS pixels.
 */

/** Smallest row height (CSS px) for a grid tile row. */
export const MIN_ROW_HEIGHT = 500;

/** Clamp a height to a whole number >= MIN_ROW_HEIGHT (invalid -> MIN_ROW_HEIGHT). */
export function clampRowHeight(h: number): number {
  if (!Number.isFinite(h)) return MIN_ROW_HEIGHT;
  return Math.max(MIN_ROW_HEIGHT, Math.floor(h));
}

/**
 * Return exactly `rowCount` row heights, each clamped to >= MIN_ROW_HEIGHT;
 * missing entries default to MIN_ROW_HEIGHT. Returns [] when rowCount <= 0.
 */
export function normalizeRowHeights(heights: readonly number[], rowCount: number): number[] {
  const count = Number.isFinite(rowCount) && rowCount > 0 ? Math.floor(rowCount) : 0;
  const out: number[] = [];
  for (let i = 0; i < count; i++) {
    out.push(clampRowHeight(heights[i] ?? MIN_ROW_HEIGHT));
  }
  return out;
}

/**
 * Set a single row's height, returning a normalized array of length `rowCount`.
 * Out-of-range indices are ignored (the normalized array is still returned).
 */
export function withRowHeight(
  heights: readonly number[],
  rowCount: number,
  rowIndex: number,
  value: number,
): number[] {
  const next = normalizeRowHeights(heights, rowCount);
  if (rowIndex >= 0 && rowIndex < next.length) {
    next[rowIndex] = clampRowHeight(value);
  }
  return next;
}
