import { afterEach, describe, expect, it, vi } from 'vitest';
import { relativeTime } from '../time';

// A fixed reference instant (Unix seconds) so every bucket boundary is exact and
// the assertions never flake near a real-clock rollover.
const NOW_SEC = 1_700_000_000;

function freezeClock(): void {
  vi.useFakeTimers();
  vi.setSystemTime(NOW_SEC * 1000);
}

afterEach(() => {
  vi.useRealTimers();
});

describe('relativeTime', () => {
  it('reports "just now" within the first minute', () => {
    freezeClock();
    expect(relativeTime(NOW_SEC)).toBe('just now');
    expect(relativeTime(NOW_SEC - 59)).toBe('just now');
  });

  it('reports whole minutes in the minute bucket', () => {
    freezeClock();
    expect(relativeTime(NOW_SEC - 60)).toBe('1m ago');
    expect(relativeTime(NOW_SEC - 5 * 60)).toBe('5m ago');
    expect(relativeTime(NOW_SEC - 59 * 60)).toBe('59m ago');
  });

  it('reports whole hours in the hour bucket', () => {
    freezeClock();
    expect(relativeTime(NOW_SEC - 3600)).toBe('1h ago');
    expect(relativeTime(NOW_SEC - 3 * 3600)).toBe('3h ago');
    expect(relativeTime(NOW_SEC - 23 * 3600)).toBe('23h ago');
  });

  it('reports whole days beyond 24h', () => {
    freezeClock();
    expect(relativeTime(NOW_SEC - 86400)).toBe('1d ago');
    expect(relativeTime(NOW_SEC - 2 * 86400)).toBe('2d ago');
  });

  it('clamps future timestamps to "just now" instead of going negative', () => {
    freezeClock();
    expect(relativeTime(NOW_SEC + 120)).toBe('just now');
  });
});