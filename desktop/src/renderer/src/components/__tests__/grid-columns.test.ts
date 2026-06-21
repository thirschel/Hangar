import { describe, it, expect } from 'vitest';
import { MIN_TILE_PX, autoColumns, cycleColumns, effectiveColumns } from '../grid-columns';

describe('MIN_TILE_PX', () => {
  it('exposes the default comfortable tile width', () => {
    expect(MIN_TILE_PX).toBe(360);
  });
});

describe('autoColumns', () => {
  it('derives multiple columns from a wide width without exceeding the tile count', () => {
    // 1200 / 360 = 3.33 -> 3 columns, with tiles to spare.
    expect(autoColumns(1200, 8)).toBe(3);
    // 1440 / 360 = 4, but only 3 tiles exist, so it caps at 3.
    expect(autoColumns(1440, 3)).toBe(3);
  });

  it('returns a single column for narrow or non-positive width when a tile exists', () => {
    expect(autoColumns(200, 4)).toBe(1); // 200 / 360 -> 0, clamped up to 1
    expect(autoColumns(0, 4)).toBe(1);
    expect(autoColumns(-500, 4)).toBe(1);
  });

  it('treats a non-finite width as a single column', () => {
    expect(autoColumns(Number.NaN, 4)).toBe(1);
    expect(autoColumns(Number.POSITIVE_INFINITY, 4)).toBe(1);
  });

  it('returns zero columns when there are no tiles', () => {
    expect(autoColumns(1920, 0)).toBe(0);
    expect(autoColumns(1920, -3)).toBe(0);
    expect(autoColumns(1920, Number.NaN)).toBe(0);
  });

  it('respects a custom minimum tile width', () => {
    // 800 / 200 = 4 columns, within the 6 available tiles.
    expect(autoColumns(800, 6, 200)).toBe(4);
    // 800 / 200 = 4, but only 2 tiles, so it caps at 2.
    expect(autoColumns(800, 2, 200)).toBe(2);
  });

  it('falls back to the default tile width when minTilePx is invalid', () => {
    expect(autoColumns(1200, 8, 0)).toBe(autoColumns(1200, 8));
    expect(autoColumns(1200, 8, -50)).toBe(autoColumns(1200, 8));
    expect(autoColumns(1200, 8, Number.NaN)).toBe(autoColumns(1200, 8));
  });
});

describe('effectiveColumns', () => {
  it('equals the Auto column count when the setting is zero', () => {
    const cases: Array<[number, number]> = [
      [1440, 3],
      [1200, 8],
      [200, 5],
      [0, 4],
    ];
    for (const [width, n] of cases) {
      expect(effectiveColumns(0, width, n)).toBe(autoColumns(width, n));
    }
  });

  it('treats negative or non-finite settings as Auto', () => {
    expect(effectiveColumns(-1, 1200, 8)).toBe(autoColumns(1200, 8));
    expect(effectiveColumns(Number.NaN, 1200, 8)).toBe(autoColumns(1200, 8));
  });

  it('clamps a positive setting to [1, n]', () => {
    expect(effectiveColumns(2, 1200, 8)).toBe(2); // already in range
    expect(effectiveColumns(99, 1200, 8)).toBe(8); // above n -> n
    expect(effectiveColumns(5, 99999, 3)).toBe(3); // above n regardless of width
  });

  it('returns zero columns when there are no tiles', () => {
    expect(effectiveColumns(0, 1200, 0)).toBe(0);
    expect(effectiveColumns(3, 1200, 0)).toBe(0);
    expect(effectiveColumns(2, 1200, -4)).toBe(0);
  });

  it('honors a custom minimum tile width in Auto mode', () => {
    expect(effectiveColumns(0, 800, 6, 200)).toBe(autoColumns(800, 6, 200));
    expect(effectiveColumns(0, 800, 6, 200)).toBe(4);
  });
});

describe('cycleColumns', () => {
  it('cycles 0 (Auto) -> 1 -> 2 -> 3 -> 0 for three tiles', () => {
    const sequence: number[] = [];
    let setting = 0;
    for (let i = 0; i < 5; i += 1) {
      setting = cycleColumns(setting, 3);
      sequence.push(setting);
    }
    expect(sequence).toEqual([1, 2, 3, 0, 1]);
  });

  it('wraps back to Auto once the setting reaches the tile count', () => {
    expect(cycleColumns(2, 3)).toBe(3);
    expect(cycleColumns(3, 3)).toBe(0);
    expect(cycleColumns(7, 3)).toBe(0); // out-of-range settings collapse to Auto
  });

  it('stays at Auto (0) when there are no tiles', () => {
    expect(cycleColumns(0, 0)).toBe(0);
    expect(cycleColumns(1, 0)).toBe(0);
    expect(cycleColumns(0, -2)).toBe(0);
  });

  it('treats negative or non-finite settings as Auto before stepping', () => {
    expect(cycleColumns(-5, 3)).toBe(1);
    expect(cycleColumns(Number.NaN, 3)).toBe(1);
  });
});
