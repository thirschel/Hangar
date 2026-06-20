import { describe, expect, it } from 'vitest';
import { analyzeBitmap } from '../paint-metrics';

// Build a width*height BGRA buffer filled with a single colour.
function solid(width: number, height: number, b: number, g: number, r: number): Uint8Array {
  const buf = new Uint8Array(width * height * 4);
  for (let i = 0; i < buf.length; i += 4) {
    buf[i] = b;
    buf[i + 1] = g;
    buf[i + 2] = r;
    buf[i + 3] = 255;
  }
  return buf;
}

describe('analyzeBitmap', () => {
  it('reports zero non-background for a uniform background region (H2: never rastered)', () => {
    const buf = solid(8, 8, 0x1e, 0x1e, 0x1e);
    const stats = analyzeBitmap(buf);
    expect(stats.sampled).toBe(64);
    expect(stats.nonBackground).toBe(0);
    expect(stats.nonBackgroundRatio).toBe(0);
  });

  it('counts non-background pixels (H1 signal: rastered but maybe not presented)', () => {
    const buf = solid(8, 8, 0xd4, 0xd4, 0xd4); // foreground grey
    const stats = analyzeBitmap(buf);
    expect(stats.nonBackground).toBe(64);
    expect(stats.nonBackgroundRatio).toBe(1);
  });

  it('treats near-background pixels within tolerance as background', () => {
    const buf = solid(4, 4, 0x21, 0x20, 0x1f); // within default tolerance 6 of #1e1e1e
    expect(analyzeBitmap(buf).nonBackground).toBe(0);
  });

  it('produces a stable hash that changes with content', () => {
    const bg = analyzeBitmap(solid(4, 4, 0x1e, 0x1e, 0x1e));
    const bg2 = analyzeBitmap(solid(4, 4, 0x1e, 0x1e, 0x1e));
    const fg = analyzeBitmap(solid(4, 4, 0xff, 0xff, 0xff));
    expect(bg.hash).toBe(bg2.hash);
    expect(bg.hash).not.toBe(fg.hash);
  });

  it('handles empty/short input without throwing', () => {
    expect(analyzeBitmap(null).sampled).toBe(0);
    expect(analyzeBitmap(new Uint8Array(2)).sampled).toBe(0);
  });

  it('samples a subset when a stride is given', () => {
    const stats = analyzeBitmap(solid(10, 10, 0xff, 0xff, 0xff), undefined, undefined, 4);
    expect(stats.sampled).toBeLessThan(100);
    expect(stats.sampled).toBeGreaterThan(0);
  });
});
