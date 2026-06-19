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
