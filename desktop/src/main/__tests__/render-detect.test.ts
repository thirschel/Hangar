import { describe, expect, it } from 'vitest';
import { isSoftwareCompositing, mergeDisableFeatures, isRemoteSession } from '../render-detect';

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

describe('mergeDisableFeatures', () => {
  it('adds the feature when the existing value is empty', () => {
    expect(mergeDisableFeatures('', ['CalculateNativeWinOcclusion'])).toBe(
      'CalculateNativeWinOcclusion',
    );
    expect(mergeDisableFeatures(undefined, ['CalculateNativeWinOcclusion'])).toBe(
      'CalculateNativeWinOcclusion',
    );
  });

  it('preserves existing features and appends new ones without clobbering', () => {
    expect(mergeDisableFeatures('SomeOtherFeature', ['CalculateNativeWinOcclusion'])).toBe(
      'SomeOtherFeature,CalculateNativeWinOcclusion',
    );
  });

  it('de-duplicates and trims, preserving first-seen order', () => {
    expect(mergeDisableFeatures(' A , B ', ['B', 'C', 'A'])).toBe('A,B,C');
    expect(mergeDisableFeatures('A,,', ['A'])).toBe('A');
  });
});

describe('isRemoteSession', () => {
  it('is true for an RDP SESSIONNAME (case-insensitive)', () => {
    expect(isRemoteSession({ SESSIONNAME: 'RDP-Tcp#0' })).toBe(true);
    expect(isRemoteSession({ SESSIONNAME: 'rdp-tcp#12' })).toBe(true);
  });

  it('is false for a console/local session', () => {
    expect(isRemoteSession({ SESSIONNAME: 'Console' })).toBe(false);
    expect(isRemoteSession({})).toBe(false);
  });

  it('honors the HANGAR_FORCE_REMOTE override', () => {
    expect(isRemoteSession({ HANGAR_FORCE_REMOTE: '1' })).toBe(true);
    expect(isRemoteSession({ HANGAR_FORCE_REMOTE: '0', SESSIONNAME: 'Console' })).toBe(false);
    expect(isRemoteSession({ HANGAR_FORCE_REMOTE: 'false' })).toBe(false);
  });
});
