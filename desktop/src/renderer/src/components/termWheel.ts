// Pure helpers for forwarding mouse-wheel events to terminal apps that drive
// their own scrolling via mouse tracking (e.g. the copilot agent's alternate-
// screen TUI). xterm forwards wheel→mouse itself, but its mouse-mode wheel
// listener cancels the event (preventDefault + stopPropagation) even when the
// forward fails on momentarily stale measurement/coords — silently dropping the
// scroll. Encoding the report ourselves guarantees the app always receives it.

// appHandlesWheel reports whether the hosted app has enabled a mouse-tracking
// mode (anything other than 'none'). When it has, the app — not the terminal —
// owns scrolling, so wheel events must be forwarded to it. Mirrors xterm's
// `Terminal.modes.mouseTrackingMode` values ('none' | 'x10' | 'vt200' | 'drag' |
// 'any'); all non-'none' DRAG/VT200/ANY protocols report the wheel event.
export function appHandlesWheel(mouseTrackingMode: string): boolean {
  return mouseTrackingMode !== 'none';
}

// encodeWheelSgr builds a single SGR mouse wheel report (button 64 = wheel up,
// 65 = wheel down) at a clamped, 1-based, in-range cell. fracX/fracY are the
// pointer position within the terminal area in [0, 1]. Returns '' when there is
// no vertical movement. SGR encoding matches the `?1006` mode copilot enables.
export function encodeWheelSgr(
  deltaY: number,
  fracX: number,
  fracY: number,
  cols: number,
  rows: number,
): string {
  if (!deltaY) return '';
  const button = deltaY < 0 ? 64 : 65;
  const col = clamp(Math.floor((toFinite(fracX)) * cols) + 1, 1, Math.max(1, cols));
  const row = clamp(Math.floor((toFinite(fracY)) * rows) + 1, 1, Math.max(1, rows));
  return `\x1b[<${button};${col};${row}M`;
}

function toFinite(value: number): number {
  return Number.isFinite(value) ? value : 0;
}

function clamp(value: number, lo: number, hi: number): number {
  if (!Number.isFinite(value)) return lo;
  return Math.min(hi, Math.max(lo, value));
}
