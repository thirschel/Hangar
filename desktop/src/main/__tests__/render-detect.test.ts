import { describe, expect, it } from 'vitest';
import { isSoftwareCompositing } from '../render-detect';

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
