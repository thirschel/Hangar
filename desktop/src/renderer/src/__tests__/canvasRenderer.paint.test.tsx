// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { CanvasTermRenderer } from '../components/canvasRenderer';

// Records the draw calls the renderer makes so we can assert it actually paints
// the buffer's glyphs (the failure mode seen on the box: canvas present but no
// text). A minimal CanvasRenderingContext2D stand-in.
function makeMockCtx(): { fillTextCalls: { text: string }[]; ctx: CanvasRenderingContext2D } {
  const fillTextCalls: { text: string }[] = [];
  const ctx = {
    save: vi.fn(),
    restore: vi.fn(),
    scale: vi.fn(),
    fillRect: vi.fn(),
    fillText: vi.fn((text: string) => {
      fillTextCalls.push({ text });
    }),
    set fillStyle(_v: string) {},
    get fillStyle(): string {
      return '';
    },
    set font(_v: string) {},
    get font(): string {
      return '';
    },
    set globalAlpha(_v: number) {},
    get globalAlpha(): number {
      return 1;
    },
    set textBaseline(_v: string) {},
    set textAlign(_v: string) {},
  } as unknown as CanvasRenderingContext2D;
  return { fillTextCalls, ctx };
}

// Builds a fake xterm Terminal whose active buffer renders the given rows of text.
// Cells are default-colored, non-styled, width-1.
function makeFakeTerminal(rows: string[], cols: number): {
  term: ConstructorParameters<typeof CanvasTermRenderer>[0];
  fireRender: () => void;
} {
  let renderCb: (() => void) | undefined;
  const makeCell = (ch: string) => ({
    getWidth: () => (ch === '' ? 0 : 1),
    getChars: () => ch,
    isInverse: () => 0,
    isBold: () => 0,
    isItalic: () => 0,
    isDim: () => 0,
    isUnderline: () => 0,
    isFgDefault: () => true,
    isFgRGB: () => false,
    isFgPalette: () => false,
    getFgColor: () => 0,
    isBgDefault: () => true,
    isBgRGB: () => false,
    isBgPalette: () => false,
    getBgColor: () => 0,
  });
  const buffer = {
    type: 'normal',
    viewportY: 0,
    baseY: 0,
    cursorX: 0,
    cursorY: 0,
    getNullCell: () => makeCell(' '),
    getLine: (y: number) => {
      const text = rows[y];
      if (text === undefined) return undefined;
      return {
        getCell: (col: number) => makeCell(text[col] ?? ' '),
      };
    },
  };
  const disposable = { dispose: vi.fn() };
  const term = {
    cols,
    rows: rows.length,
    buffer: { active: buffer },
    getSelectionPosition: () => undefined,
    onRender: (cb: () => void) => {
      renderCb = cb;
      return disposable;
    },
    onScroll: () => disposable,
    onResize: () => disposable,
    onCursorMove: () => disposable,
    onSelectionChange: () => disposable,
  } as unknown as ConstructorParameters<typeof CanvasTermRenderer>[0];
  return { term, fireRender: () => renderCb?.() };
}

describe('CanvasTermRenderer paint', () => {
  let mock: ReturnType<typeof makeMockCtx>;

  beforeEach(() => {
    mock = makeMockCtx();
    vi.spyOn(HTMLCanvasElement.prototype, 'getContext').mockReturnValue(
      mock.ctx as unknown as ReturnType<HTMLCanvasElement['getContext']>,
    );
    vi.stubGlobal(
      'ResizeObserver',
      class {
        observe(): void {}
        unobserve(): void {}
        disconnect(): void {}
      },
    );
  });

  afterEach(() => {
    vi.restoreAllMocks();
    vi.unstubAllGlobals();
  });

  it('draws the buffer glyphs to the canvas on construction', () => {
    const container = document.createElement('div');
    const { term } = makeFakeTerminal(['hi', '', 'yo'], 2);

    const renderer = new CanvasTermRenderer(term, container, {
      theme: {
        background: '#1e1e1e',
        foreground: '#d4d4d4',
        cursor: '#ffffff',
        selectionBackground: '#264f78',
      },
      fontFamily: 'monospace',
      fontSize: 13,
    });

    const drawn = mock.fillTextCalls.map((c) => c.text).join('');
    // Non-space glyphs from rows 0 and 2 ('hi','yo'); spaces are skipped. The cursor
    // pass redraws the glyph under the cursor (cell 0,0 = 'h'), so there is one extra
    // fillText beyond the 4 content glyphs.
    expect(drawn).toContain('h');
    expect(drawn).toContain('i');
    expect(drawn).toContain('y');
    expect(drawn).toContain('o');
    expect(mock.fillTextCalls.length).toBeGreaterThanOrEqual(4);

    renderer.dispose();
  });

  it('reports glyphsDrawn via the log callback', () => {
    const container = document.createElement('div');
    const { term } = makeFakeTerminal(['abc'], 3);
    const logs: { event: string; data?: unknown }[] = [];

    const renderer = new CanvasTermRenderer(term, container, {
      theme: {
        background: '#1e1e1e',
        foreground: '#d4d4d4',
        cursor: '#ffffff',
        selectionBackground: '#264f78',
      },
      fontFamily: 'monospace',
      fontSize: 13,
      log: (event, data) => logs.push({ event, data }),
    });

    const paintLog = logs.find((l) => l.event === 'canvas paint');
    expect(paintLog).toBeDefined();
    expect((paintLog?.data as { glyphsDrawn: number }).glyphsDrawn).toBe(3);

    renderer.dispose();
  });
});
