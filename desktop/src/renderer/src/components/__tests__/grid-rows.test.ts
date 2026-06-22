import { describe, expect, it } from 'vitest';
import { MIN_ROW_HEIGHT, clampRowHeight, normalizeRowHeights, withRowHeight } from '../grid-rows';

describe('grid-rows', () => {
  it('exposes a 500px minimum', () => {
    expect(MIN_ROW_HEIGHT).toBe(500);
  });

  it('clampRowHeight floors and enforces the minimum', () => {
    expect(clampRowHeight(700.6)).toBe(700);
    expect(clampRowHeight(300)).toBe(500);
    expect(clampRowHeight(500)).toBe(500);
    expect(clampRowHeight(Number.NaN)).toBe(500);
  });

  it('normalizeRowHeights returns exactly rowCount clamped entries', () => {
    expect(normalizeRowHeights([700, 200], 3)).toEqual([700, 500, 500]);
    expect(normalizeRowHeights([800], 0)).toEqual([]);
    expect(normalizeRowHeights([], 2)).toEqual([500, 500]);
  });

  it('withRowHeight sets one row and clamps to the floor', () => {
    expect(withRowHeight([500, 500, 500], 3, 1, 720)).toEqual([500, 720, 500]);
    expect(withRowHeight([500, 500], 2, 0, 100)).toEqual([500, 500]);
  });

  it('withRowHeight ignores out-of-range indices', () => {
    expect(withRowHeight([500, 500], 2, 5, 900)).toEqual([500, 500]);
    expect(withRowHeight([500, 500], 2, -1, 900)).toEqual([500, 500]);
  });

  it('does not mutate the input array', () => {
    const input = [500, 500];
    withRowHeight(input, 2, 0, 800);
    expect(input).toEqual([500, 500]);
  });
});
