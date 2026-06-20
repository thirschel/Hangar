import { describe, expect, it } from 'vitest';
import {
  getRenderInfo,
  isRemoteSession,
  isSoftwareCompositing,
  parseRenderState,
  serializeRenderState,
} from '../render-detect';

describe('isSoftwareCompositing', () => {
  it('is true when hardware acceleration is explicitly disabled', () => {
    expect(isSoftwareCompositing({ gpu_compositing: 'enabled' }, true)).toBe(true);
    expect(isSoftwareCompositing(null, true)).toBe(true);
  });

  it('is true under RDP/software compositing (disabled_software)', () => {
    expect(isSoftwareCompositing({ gpu_compositing: 'disabled_software' }, false)).toBe(true);
    expect(isSoftwareCompositing({ gpu_compositing: 'disabled_off' }, false)).toBe(true);
    expect(isSoftwareCompositing({ gpu_compositing: 'software' }, false)).toBe(true);
  });

  it('is false on a normal GPU machine (enabled compositing)', () => {
    expect(isSoftwareCompositing({ gpu_compositing: 'enabled' }, false)).toBe(false);
    expect(isSoftwareCompositing({ gpu_compositing: 'enabled_on' }, false)).toBe(false);
  });

  it('treats unknown/empty status as GPU (no forced repaints by mistake)', () => {
    expect(isSoftwareCompositing({}, false)).toBe(false);
    expect(isSoftwareCompositing(null, false)).toBe(false);
    expect(isSoftwareCompositing(undefined, false)).toBe(false);
  });
});

describe('isRemoteSession', () => {
  it('detects an RDP transport SESSIONNAME', () => {
    expect(isRemoteSession({ SESSIONNAME: 'RDP-Tcp#3' })).toBe(true);
    expect(isRemoteSession({ SESSIONNAME: 'rdp-tcp#12' })).toBe(true);
  });

  it('is false for a local console session', () => {
    expect(isRemoteSession({ SESSIONNAME: 'Console' })).toBe(false);
    expect(isRemoteSession({})).toBe(false);
    expect(isRemoteSession(null)).toBe(false);
    expect(isRemoteSession(undefined)).toBe(false);
  });

  it('honours an explicit SM_REMOTESESSION hint regardless of name', () => {
    expect(isRemoteSession({ SESSIONNAME: 'Console' }, true)).toBe(true);
    expect(isRemoteSession({}, true)).toBe(true);
  });
});

describe('getRenderInfo', () => {
  it('composes compositing + remote detection and records the session name', () => {
    const info = getRenderInfo({
      featureStatus: { gpu_compositing: 'disabled_software' },
      hardwareAccelerationDisabled: false,
      windowOcclusionDisabled: true,
      env: { SESSIONNAME: 'RDP-Tcp#7' },
    });
    expect(info).toEqual({
      softwareCompositing: true,
      remoteSession: true,
      sessionName: 'RDP-Tcp#7',
      hardwareAccelerationDisabled: false,
      windowOcclusionDisabled: true,
    });
  });

  it('reports a normal GPU console machine as neither software nor remote', () => {
    const info = getRenderInfo({
      featureStatus: { gpu_compositing: 'enabled' },
      hardwareAccelerationDisabled: false,
      windowOcclusionDisabled: false,
      env: { SESSIONNAME: 'Console' },
    });
    expect(info.softwareCompositing).toBe(false);
    expect(info.remoteSession).toBe(false);
  });
});

describe('render-state cache', () => {
  it('round-trips through serialize/parse', () => {
    const info = getRenderInfo({
      featureStatus: { gpu_compositing: 'disabled_software' },
      hardwareAccelerationDisabled: false,
      windowOcclusionDisabled: true,
      env: { SESSIONNAME: 'RDP-Tcp#1' },
    });
    const raw = serializeRenderState(info, new Date('2026-01-02T03:04:05.000Z'));
    const parsed = parseRenderState(raw);
    expect(parsed).toEqual({
      softwareCompositing: true,
      remoteSession: true,
      sessionName: 'RDP-Tcp#1',
      updatedAt: '2026-01-02T03:04:05.000Z',
    });
  });

  it('returns null for malformed/empty input', () => {
    expect(parseRenderState(null)).toBeNull();
    expect(parseRenderState('')).toBeNull();
    expect(parseRenderState('not json')).toBeNull();
  });
});
