// Pure, electron-free helpers for detecting Chromium's compositing mode, so they
// can be unit-tested without booting the Electron app.

// isSoftwareCompositing reports whether Chromium is compositing in software (no
// GPU). On such configs — most importantly RDP/VDI sessions, where there is no
// hardware GPU — the terminal's xterm DOM updates but the software compositor
// does not always flush the paint, so the pane stays blank until a reflow (a
// manual window/pane resize). Derived from Electron's app.getGPUFeatureStatus()
// plus the explicit disable-hardware-acceleration setting.
export function isSoftwareCompositing(
  featureStatus: Record<string, unknown> | null | undefined,
  hwAccelDisabled: boolean,
): boolean {
  if (hwAccelDisabled) return true;
  const gc = String(featureStatus?.gpu_compositing ?? '');
  // "enabled"/"enabled_on" means GPU compositing; anything else
  // (disabled_software, disabled_off, software, unavailable, …) means
  // software/no compositing. An empty/unknown value is treated as GPU (no
  // workaround) so we never force repaints on a normal machine by mistake.
  return gc !== '' && !gc.startsWith('enabled');
}

// mergeDisableFeatures composes a Chromium `--disable-features` value without
// clobbering features another part of the app (or Chromium itself) already set.
// It merges the existing comma-separated list with `additions`, de-duplicates,
// preserves first-seen order, and drops empties. Returns a comma-joined string.
export function mergeDisableFeatures(existing: string | undefined, additions: string[]): string {
  const seen = new Set<string>();
  const out: string[] = [];
  for (const part of [...String(existing ?? '').split(','), ...additions]) {
    const name = part.trim();
    if (name && !seen.has(name)) {
      seen.add(name);
      out.push(name);
    }
  }
  return out.join(',');
}

// isRemoteSession reports whether the process is (heuristically) running in a
// remote/RDP session. Pure Windows-friendly heuristic: RDP/VDI sessions set
// SESSIONNAME to an "RDP-Tcp#…" value (console sessions use "Console"). An
// explicit HANGAR_FORCE_REMOTE override (any non-empty, non-"0" value) wins so
// the affected box can force the remote path even when SESSIONNAME is unset by a
// third-party remote tool. Best-effort only; back it with cached render info.
export function isRemoteSession(env: NodeJS.ProcessEnv = process.env): boolean {
  const override = String(env.HANGAR_FORCE_REMOTE ?? '').trim();
  if (override && override !== '0' && override.toLowerCase() !== 'false') return true;
  const sessionName = String(env.SESSIONNAME ?? '').trim();
  return /^rdp-/i.test(sessionName);
}
