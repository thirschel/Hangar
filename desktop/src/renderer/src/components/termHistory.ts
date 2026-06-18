export function isAtBottom(viewportY: number, baseY: number): boolean {
  return viewportY >= baseY;
}

// Reconciliation is useful while the user is reviewing history and the host
// scrollback has grown. Once the host reaches its scrollback cap this signal
// plateaus, so capped-history churn is an acceptable v1 limitation.
export function shouldReconcile(
  prevScrollbackLines: number,
  curScrollbackLines: number,
  atBottom: boolean,
): boolean {
  return !atBottom && curScrollbackLines > prevScrollbackLines;
}

export function normalizeHistory(ansi: string): string {
  if (!ansi) return '';
  return `${ansi.replace(/(?:\r?\n)+$/, '')}\n`;
}
