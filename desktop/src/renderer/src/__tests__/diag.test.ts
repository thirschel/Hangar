// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from 'vitest';
import { diag } from '../diag';

describe('diag bridge', () => {
  afterEach(() => {
    vi.restoreAllMocks();
  });

  it('forwards event, data, and level to window.cs.diag', () => {
    const spy = vi.fn();
    window.cs.diag = spy;

    diag('some event', { a: 1 }, 'error');

    expect(spy).toHaveBeenCalledWith('some event', { a: 1 }, 'error');
  });

  it('defaults the level to info', () => {
    const spy = vi.fn();
    window.cs.diag = spy;

    diag('plain');

    expect(spy).toHaveBeenCalledWith('plain', undefined, 'info');
  });

  it('never throws when the bridge is unavailable', () => {
    // Simulate preload not exposing diag (e.g. older preload / test harness).
    const original = window.cs.diag;
    (window.cs as { diag?: unknown }).diag = undefined;

    expect(() => diag('safe')).not.toThrow();

    window.cs.diag = original;
  });
});
