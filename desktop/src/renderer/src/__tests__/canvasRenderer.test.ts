import { describe, expect, it } from 'vitest';
import {
  build256Palette,
  rgbFromPacked,
  resolveCellColor,
  isCellSelected,
  type CellColorSource,
  type TermTheme,
} from '../components/canvasRenderer';

const theme: TermTheme = {
  background: '#1e1e1e',
  foreground: '#d4d4d4',
  cursor: '#ffffff',
  selectionBackground: '#264f78',
};

describe('build256Palette', () => {
  it('produces 256 entries with the ANSI-16 at the front', () => {
    const ansi16 = Array.from({ length: 16 }, (_, i) => `#${i.toString(16)}`);
    const pal = build256Palette(ansi16);
    expect(pal).toHaveLength(256);
    expect(pal[0]).toBe('#0');
    expect(pal[15]).toBe('#f');
  });

  it('computes the 6x6x6 color cube and grayscale ramp', () => {
    const pal = build256Palette();
    // 16 = first cube entry (0,0,0); 231 = last cube entry (255,255,255).
    expect(pal[16]).toBe('rgb(0, 0, 0)');
    expect(pal[231]).toBe('rgb(255, 255, 255)');
    // 232 = first grayscale (8); 255 = last grayscale (238).
    expect(pal[232]).toBe('rgb(8, 8, 8)');
    expect(pal[255]).toBe('rgb(238, 238, 238)');
  });
});

describe('rgbFromPacked', () => {
  it('unpacks 0xRRGGBB into a CSS rgb string', () => {
    expect(rgbFromPacked(0xff0000)).toBe('rgb(255, 0, 0)');
    expect(rgbFromPacked(0x00ff00)).toBe('rgb(0, 255, 0)');
    expect(rgbFromPacked(0x123456)).toBe('rgb(18, 52, 86)');
  });
});

function cell(overrides: Partial<CellColorSource>): CellColorSource {
  return {
    isFgDefault: () => true,
    isFgRGB: () => false,
    isFgPalette: () => false,
    getFgColor: () => 0,
    isBgDefault: () => true,
    isBgRGB: () => false,
    isBgPalette: () => false,
    getBgColor: () => 0,
    ...overrides,
  };
}

describe('resolveCellColor', () => {
  const palette = build256Palette();

  it('returns theme colors for default cells', () => {
    expect(resolveCellColor(cell({}), 'fg', theme, palette)).toBe(theme.foreground);
    expect(resolveCellColor(cell({}), 'bg', theme, palette)).toBe(theme.background);
  });

  it('returns truecolor for RGB cells', () => {
    const c = cell({ isFgDefault: () => false, isFgRGB: () => true, getFgColor: () => 0x102030 });
    expect(resolveCellColor(c, 'fg', theme, palette)).toBe('rgb(16, 32, 48)');
  });

  it('indexes the palette for palette cells', () => {
    const c = cell({ isBgDefault: () => false, isBgPalette: () => true, getBgColor: () => 196 });
    expect(resolveCellColor(c, 'bg', theme, palette)).toBe(palette[196]);
  });

  it('falls back to theme when a palette index is out of range', () => {
    const c = cell({ isFgDefault: () => false, isFgPalette: () => true, getFgColor: () => 999 });
    expect(resolveCellColor(c, 'fg', theme, palette)).toBe(theme.foreground);
  });
});

describe('isCellSelected', () => {
  it('is false with no selection', () => {
    expect(isCellSelected(undefined, 5, 3)).toBe(false);
  });

  it('handles a single-row selection (inclusive start, exclusive end)', () => {
    const sel = { start: { x: 2, y: 4 }, end: { x: 6, y: 4 } };
    expect(isCellSelected(sel, 4, 1)).toBe(false);
    expect(isCellSelected(sel, 4, 2)).toBe(true);
    expect(isCellSelected(sel, 4, 5)).toBe(true);
    expect(isCellSelected(sel, 4, 6)).toBe(false);
  });

  it('handles a multi-row selection', () => {
    const sel = { start: { x: 3, y: 4 }, end: { x: 2, y: 6 } };
    expect(isCellSelected(sel, 4, 2)).toBe(false); // before start col on first row
    expect(isCellSelected(sel, 4, 3)).toBe(true);
    expect(isCellSelected(sel, 5, 0)).toBe(true); // whole middle row
    expect(isCellSelected(sel, 5, 99)).toBe(true);
    expect(isCellSelected(sel, 6, 1)).toBe(true);
    expect(isCellSelected(sel, 6, 2)).toBe(false); // at/after end col on last row
    expect(isCellSelected(sel, 7, 0)).toBe(false); // past end row
  });
});
