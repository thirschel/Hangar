/**
 * Pure "agents per row" math for the multi-agent grid view.
 *
 * Mirrors the Go TUI grid helper (see ui/grid.go: `EffectiveColumns` /
 * `CycleColumns`) so the desktop grid view and the terminal grid view stay in
 * lock-step. Intentionally free of React, DOM and IPC dependencies, and free of
 * side effects.
 */

/** Smallest comfortable tile width, in CSS pixels, used for the Auto column math. */
export const MIN_TILE_PX = 360;

/** Coerce a tile count to a non-negative integer; invalid values become 0. */
function tileCount(n: number): number {
  if (!Number.isFinite(n) || n <= 0) return 0;
  return Math.floor(n);
}

/** Coerce the minimum tile width to a positive number, defaulting when invalid. */
function tileWidthPx(minTilePx: number): number {
  if (!Number.isFinite(minTilePx) || minTilePx <= 0) return MIN_TILE_PX;
  return minTilePx;
}

/**
 * Auto column count derived from the available width and tile count.
 *
 * Returns `floor(width / minTilePx)` clamped to `[1, n]`. A non-positive or
 * non-finite width collapses to a single column (when there is at least one
 * tile); `n <= 0` yields 0 columns.
 *
 * @param width - Available width in CSS pixels.
 * @param n - Number of tiles to lay out.
 * @param minTilePx - Smallest comfortable tile width; defaults to {@link MIN_TILE_PX}.
 * @returns The Auto column count.
 */
export function autoColumns(width: number, n: number, minTilePx: number = MIN_TILE_PX): number {
  const tiles = tileCount(n);
  if (tiles <= 0) return 0;
  const tilePx = tileWidthPx(minTilePx);
  const cols = Number.isFinite(width) && width > 0 ? Math.floor(width / tilePx) : 0;
  return Math.max(1, Math.min(cols, tiles));
}

/**
 * Column count actually used to lay out the grid.
 *
 * A `setting` of 0 — or any non-positive / non-finite value — means Auto and
 * defers to {@link autoColumns}. A positive `setting` is clamped to `[1, n]`.
 * Returns 0 columns when `n <= 0`.
 *
 * @param setting - Raw "agents per row" override (<= 0 means Auto).
 * @param width - Available width in CSS pixels.
 * @param n - Number of tiles to lay out.
 * @param minTilePx - Smallest comfortable tile width; defaults to {@link MIN_TILE_PX}.
 * @returns The effective column count.
 */
export function effectiveColumns(
  setting: number,
  width: number,
  n: number,
  minTilePx: number = MIN_TILE_PX,
): number {
  const tiles = tileCount(n);
  if (tiles <= 0) return 0;
  if (!Number.isFinite(setting) || setting <= 0) {
    return autoColumns(width, tiles, minTilePx);
  }
  return Math.max(1, Math.min(Math.floor(setting), tiles));
}

/**
 * Cycle the raw "agents per row" setting: 0 (Auto) -> 1 -> 2 -> ... -> n -> 0,
 * wrapping back to Auto once it passes the tile count.
 *
 * Negative, non-finite or out-of-range settings are treated as Auto (0) before
 * stepping. When `n <= 0` the result stays 0.
 *
 * @param setting - Current raw setting.
 * @param n - Number of tiles (the wrap point).
 * @returns The next raw setting.
 */
export function cycleColumns(setting: number, n: number): number {
  const tiles = tileCount(n);
  const current = Number.isFinite(setting) && setting > 0 ? Math.floor(setting) : 0;
  const next = current + 1;
  return next > tiles ? 0 : next;
}
