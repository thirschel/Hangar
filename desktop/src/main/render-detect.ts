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

// isRemoteSession reports whether the process is running inside a Windows Remote
// Desktop / Terminal Services session. RDP sessions have no hardware GPU, so they
// composite in software and are the environment where the blank-terminal bug
// reproduces. We detect from the environment (electron-free, unit-testable):
//   - SESSIONNAME like "RDP-Tcp#3" (the classic RDP transport),
//   - an explicit SM_REMOTESESSION hint the caller may inject from Electron's
//     systemPreferences (e.g. "1"/"true") when available.
// A local console session sets SESSIONNAME to "Console" (or leaves it unset).
export function isRemoteSession(
  env: Record<string, string | undefined> | null | undefined,
  smRemoteSession?: boolean,
): boolean {
  if (smRemoteSession) return true;
  const name = String(env?.SESSIONNAME ?? '').trim();
  if (!name) return false;
  return /^rdp-/i.test(name);
}

// RenderInfo is the resolved view of how this machine paints, used to gate
// startup mitigations and to log a single decisive line for blank-terminal
// reports. softwareCompositing/remoteSession are the two H1 (native-present)
// signals; sessionName is recorded verbatim for triage.
export type RenderInfo = {
  softwareCompositing: boolean;
  remoteSession: boolean;
  sessionName: string;
  hardwareAccelerationDisabled: boolean;
  windowOcclusionDisabled: boolean;
};

// getRenderInfo composes the individual detectors into the resolved RenderInfo.
// Pure (no Electron, no fs) so it can be unit-tested; the caller supplies the
// GPU feature status and environment.
export function getRenderInfo(args: {
  featureStatus: Record<string, unknown> | null | undefined;
  hardwareAccelerationDisabled: boolean;
  windowOcclusionDisabled: boolean;
  env?: Record<string, string | undefined> | null;
  smRemoteSession?: boolean;
}): RenderInfo {
  return {
    softwareCompositing: isSoftwareCompositing(args.featureStatus, args.hardwareAccelerationDisabled),
    remoteSession: isRemoteSession(args.env, args.smRemoteSession),
    sessionName: String(args.env?.SESSIONNAME ?? '').trim(),
    hardwareAccelerationDisabled: args.hardwareAccelerationDisabled,
    windowOcclusionDisabled: args.windowOcclusionDisabled,
  };
}

// RenderStateCache is persisted across launches (in ~/.hangar) so the next start
// can gate PRE-READY command-line switches (which must be set before the GPU is
// initialized, when getGPUFeatureStatus is not yet available) on the previously
// observed compositing/remote state. Phase 0 only records it; later phases read
// it to decide whether to opt into renderer-mode switches at launch.
export type RenderStateCache = {
  softwareCompositing: boolean;
  remoteSession: boolean;
  sessionName: string;
  updatedAt: string;
};

// parseRenderState safely reads a persisted cache (electron-free, never throws).
export function parseRenderState(raw: string | null | undefined): RenderStateCache | null {
  if (!raw) return null;
  try {
    const o = JSON.parse(raw) as Partial<RenderStateCache>;
    if (typeof o !== 'object' || o === null) return null;
    return {
      softwareCompositing: Boolean(o.softwareCompositing),
      remoteSession: Boolean(o.remoteSession),
      sessionName: String(o.sessionName ?? ''),
      updatedAt: String(o.updatedAt ?? ''),
    };
  } catch {
    return null;
  }
}

// serializeRenderState renders a cache entry from the resolved RenderInfo.
export function serializeRenderState(info: RenderInfo, now: Date = new Date()): string {
  const cache: RenderStateCache = {
    softwareCompositing: info.softwareCompositing,
    remoteSession: info.remoteSession,
    sessionName: info.sessionName,
    updatedAt: now.toISOString(),
  };
  return JSON.stringify(cache, null, 2) + '\n';
}
