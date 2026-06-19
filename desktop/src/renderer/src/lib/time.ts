/**
 * Format a Unix timestamp (seconds) as a short, human-readable "time ago"
 * string, e.g. "just now", "5m ago", "3h ago", "2d ago".
 *
 * Moved verbatim from SessionBrowserModal so both the session browser and the
 * sidebar's "last agent output" indicator share one implementation.
 */
export function relativeTime(unix: number): string {
  const diff = Math.max(0, Math.floor(Date.now() / 1000 - unix));
  if (diff < 60) return 'just now';
  if (diff < 3600) return `${Math.floor(diff / 60)}m ago`;
  if (diff < 86400) return `${Math.floor(diff / 3600)}h ago`;
  return `${Math.floor(diff / 86400)}d ago`;
}