// Client-side canvas terminal renderer.
//
// On RDP / no-GPU (software-compositing) machines, Chromium's software compositor
// does not present xterm's DOM renderer (hundreds of absolutely-positioned <span>s)
// to the screen, so the terminal pane stays blank — but a single 2D <canvas>
// *does* present (verified on the affected box). This renderer keeps xterm for
// parsing, the buffer model, input, selection and scrollback, and replaces only the
// PAINT step: it reads the visible buffer cells via xterm's public API and draws
// them onto one opaque <canvas> overlaid on the (covered) DOM rows.
//
// It is intentionally minimal (monospace text, fg/bg colors, bold/italic/dim/
// underline/inverse, block cursor, selection highlight, scrollback). It does not do
// ligatures, decorations/links, or blink. See docs/rdp-blank-terminal-postmortem.md.

import type { IDisposable, Terminal } from '@xterm/xterm';

export type TermTheme = {
  background: string;
  foreground: string;
  cursor: string;
  selectionBackground: string;
};

// Standard xterm-compatible 16-color ANSI palette (indices 0-15). Used to resolve
// palette-mode cell colors; the theme's fg/bg cover default-mode cells.
const ANSI_16 = [
  '#2e3436', '#cc0000', '#4e9a06', '#c4a000', '#3465a4', '#75507b', '#06989a', '#d3d7cf',
  '#555753', '#ef2929', '#8ae234', '#fce94f', '#729fcf', '#ad7fa8', '#34e2e2', '#eeeeec',
];

// build256Palette returns CSS color strings for xterm palette indices 0-255:
// 0-15 ANSI, 16-231 the 6x6x6 color cube, 232-255 the grayscale ramp.
export function build256Palette(ansi16: readonly string[] = ANSI_16): string[] {
  const palette: string[] = [...ansi16];
  const levels = [0, 95, 135, 175, 215, 255];
  for (let r = 0; r < 6; r++) {
    for (let g = 0; g < 6; g++) {
      for (let b = 0; b < 6; b++) {
        palette.push(`rgb(${levels[r]}, ${levels[g]}, ${levels[b]})`);
      }
    }
  }
  for (let i = 0; i < 24; i++) {
    const v = 8 + i * 10;
    palette.push(`rgb(${v}, ${v}, ${v})`);
  }
  return palette;
}

// rgbFromPacked converts a 24-bit 0xRRGGBB number to a CSS rgb() string.
export function rgbFromPacked(value: number): string {
  const r = (value >> 16) & 0xff;
  const g = (value >> 8) & 0xff;
  const b = value & 0xff;
  return `rgb(${r}, ${g}, ${b})`;
}

// CellColorSource is the subset of IBufferCell color methods resolveCellColor needs;
// declaring it lets the resolver be unit-tested without a real xterm buffer.
export type CellColorSource = {
  isFgDefault(): boolean;
  isFgRGB(): boolean;
  isFgPalette(): boolean;
  getFgColor(): number;
  isBgDefault(): boolean;
  isBgRGB(): boolean;
  isBgPalette(): boolean;
  getBgColor(): number;
};

// resolveCellColor maps a cell's foreground or background to a CSS color string,
// honoring default (theme), RGB (truecolor) and palette (256) color modes.
export function resolveCellColor(
  cell: CellColorSource,
  which: 'fg' | 'bg',
  theme: TermTheme,
  palette: readonly string[],
): string {
  if (which === 'fg') {
    if (cell.isFgDefault()) return theme.foreground;
    if (cell.isFgRGB()) return rgbFromPacked(cell.getFgColor());
    if (cell.isFgPalette()) return palette[cell.getFgColor()] ?? theme.foreground;
    return theme.foreground;
  }
  if (cell.isBgDefault()) return theme.background;
  if (cell.isBgRGB()) return rgbFromPacked(cell.getBgColor());
  if (cell.isBgPalette()) return palette[cell.getBgColor()] ?? theme.background;
  return theme.background;
}

export class CanvasTermRenderer {
  private readonly term: Terminal;
  private readonly container: HTMLElement;
  private readonly theme: TermTheme;
  private readonly fontFamily: string;
  private readonly fontSize: number;
  private readonly palette: string[];
  private readonly log?: (event: string, data?: unknown) => void;
  private readonly catchupTimers: ReturnType<typeof setTimeout>[] = [];
  private paintLogCount = 0;
  private lastPaintLog = 0;
  private readonly canvas: HTMLCanvasElement;
  private readonly ctx: CanvasRenderingContext2D | null;
  private readonly disposables: IDisposable[] = [];
  private readonly onWindowResize: () => void;
  private readonly onFocusIn: () => void;
  private readonly onFocusOut: () => void;
  private readonly resizeObserver: ResizeObserver;
  private reusableCell: ReturnType<Terminal['buffer']['active']['getNullCell']> | null = null;
  private lastFont = '';
  private focused = true;
  private cellW = 8;
  private cellH = 16;
  private dpr = 1;
  private rafPending = false;
  private resizePending = false;
  private disposed = false;

  constructor(
    term: Terminal,
    container: HTMLElement,
    opts: {
      theme: TermTheme;
      fontFamily: string;
      fontSize: number;
      log?: (event: string, data?: unknown) => void;
    },
  ) {
    this.term = term;
    this.container = container;
    this.theme = opts.theme;
    this.fontFamily = opts.fontFamily;
    this.fontSize = opts.fontSize;
    this.log = opts.log;
    this.palette = build256Palette();

    const canvas = document.createElement('canvas');
    canvas.className = 'agent-terminal__canvas';
    // Opaque overlay covering the (non-presenting) xterm DOM rows. pointer-events
    // none so mouse selection still reaches xterm underneath.
    canvas.style.cssText =
      'position:absolute;inset:0;width:100%;height:100%;z-index:5;pointer-events:none;';
    if (!this.container.style.position) this.container.style.position = 'relative';
    this.container.appendChild(canvas);
    this.canvas = canvas;
    this.ctx = canvas.getContext('2d');

    this.onWindowResize = () => this.scheduleResize();
    // Cursor hollows/hides when the pane loses focus. focusin/focusout bubble from
    // xterm's hidden textarea to the container.
    this.onFocusIn = () => this.setFocused(true);
    this.onFocusOut = () => this.setFocused(false);

    this.disposables.push(term.onRender(() => this.schedulePaint()));
    this.disposables.push(term.onScroll(() => this.schedulePaint()));
    this.disposables.push(term.onResize(() => this.scheduleResize()));
    this.disposables.push(term.onCursorMove(() => this.schedulePaint()));
    this.disposables.push(term.onSelectionChange(() => this.schedulePaint()));
    window.addEventListener('resize', this.onWindowResize);
    container.addEventListener('focusin', this.onFocusIn);
    container.addEventListener('focusout', this.onFocusOut);
    // Catch sub-cell container resizes (splitter drags, panel toggles) that don't
    // change cols/rows — without this the canvas would stretch a stale bitmap and
    // blur until the next cols/rows change.
    this.resizeObserver = new ResizeObserver(() => this.scheduleResize());
    this.resizeObserver.observe(container);

    this.resize();
    // The agent's initial draw may land before/around construction and may not
    // emit a further onRender; force a few catch-up paints so the first screen is
    // never missed. (rafPending coalescing keeps these cheap.)
    for (const delay of [150, 500, 1200, 2500]) {
      const t = setTimeout(() => this.schedulePaint(), delay);
      t.unref?.();
      this.catchupTimers.push(t);
    }
  }

  // requestPaint schedules a repaint; call it after writing data so the canvas
  // always reflects the latest buffer even if onRender timing is missed.
  requestPaint(): void {
    this.schedulePaint();
  }

  setFocused(focused: boolean): void {
    this.focused = focused;
    this.schedulePaint();
  }

  // resize re-derives the canvas backing size (DPR-aware) and the per-cell size from
  // the current pane rect and xterm's cols/rows, then repaints.
  resize(): void {
    if (this.disposed) return;
    const rect = this.container.getBoundingClientRect();
    const cssW = Math.max(1, Math.round(rect.width));
    const cssH = Math.max(1, Math.round(rect.height));
    this.dpr = window.devicePixelRatio || 1;
    this.canvas.width = Math.round(cssW * this.dpr);
    this.canvas.height = Math.round(cssH * this.dpr);
    this.cellW = cssW / Math.max(1, this.term.cols);
    this.cellH = cssH / Math.max(1, this.term.rows);
    this.paint();
  }

  private schedulePaint(): void {
    if (this.disposed || this.rafPending) return;
    this.rafPending = true;
    requestAnimationFrame(() => {
      this.rafPending = false;
      this.paint();
    });
  }

  // scheduleResize coalesces resize bursts (ResizeObserver fires continuously during
  // splitter drags) to one re-size + repaint per frame.
  private scheduleResize(): void {
    if (this.disposed || this.resizePending) return;
    this.resizePending = true;
    requestAnimationFrame(() => {
      this.resizePending = false;
      this.resize();
    });
  }

  private paint(): void {
    const ctx = this.ctx;
    if (this.disposed || !ctx) return;
    const buffer = this.term.buffer.active;
    const cols = this.term.cols;
    const rows = this.term.rows;
    const cw = this.cellW;
    const ch = this.cellH;

    ctx.save();
    ctx.scale(this.dpr, this.dpr);
    // Clear with the background color (one opaque tile the SW compositor presents).
    ctx.fillStyle = this.theme.background;
    ctx.fillRect(0, 0, this.canvas.width / this.dpr, this.canvas.height / this.dpr);
    ctx.textBaseline = 'middle';
    ctx.textAlign = 'left';
    // Font is reset by save(); track it so we only reassign when the style changes
    // (per-cell ctx.font assignment is a real cost on the no-GPU target).
    this.lastFont = '';
    const underlineH = Math.max(1 / this.dpr, 1);

    const selection = this.term.getSelectionPosition();
    // Reuse one cell object across the whole grid to avoid per-cell allocation.
    const reuse = (this.reusableCell ??= buffer.getNullCell());
    let glyphsDrawn = 0;

    for (let row = 0; row < rows; row++) {
      const line = buffer.getLine(buffer.viewportY + row);
      if (!line) continue;
      const y = row * ch;
      for (let col = 0; col < cols; col++) {
        const cell = line.getCell(col, reuse);
        if (!cell) continue;
        const width = cell.getWidth();
        if (width === 0) continue; // trailing half of a wide char
        const x = col * cw;
        const cellPxW = cw * width;

        const inverse = !!cell.isInverse();
        let fg = resolveCellColor(cell, 'fg', this.theme, this.palette);
        let bg = resolveCellColor(cell, 'bg', this.theme, this.palette);
        if (inverse) {
          const t = fg;
          fg = bg;
          bg = t;
        }

        const selected = isCellSelected(selection, buffer.viewportY + row, col);
        if (selected) {
          bg = this.theme.selectionBackground;
        }

        if (bg !== this.theme.background || selected) {
          ctx.fillStyle = bg;
          ctx.fillRect(x, y, cellPxW + 0.5, ch + 0.5);
        }

        const chars = cell.getChars();
        if (chars && chars !== ' ') {
          const bold = !!cell.isBold();
          const italic = !!cell.isItalic();
          const font = `${italic ? 'italic ' : ''}${bold ? 'bold ' : ''}${this.fontSize}px ${this.fontFamily}`;
          if (font !== this.lastFont) {
            ctx.font = font;
            this.lastFont = font;
          }
          ctx.globalAlpha = cell.isDim() ? 0.6 : 1;
          ctx.fillStyle = fg;
          ctx.fillText(chars, x, y + ch / 2);
          ctx.globalAlpha = 1;
          glyphsDrawn += 1;
          if (cell.isUnderline()) {
            ctx.fillRect(x, y + ch - underlineH, cellPxW, underlineH);
          }
        }
      }
    }

    this.paintCursor(ctx, buffer, cw, ch);
    ctx.restore();

    // Diagnostic: report what the canvas actually drew so a blank pane can be
    // diagnosed from desktop.log (glyphsDrawn===0 ⇒ empty buffer / paint missed;
    // glyphsDrawn>0 but screen blank ⇒ a present/geometry issue). First few paints
    // then throttled to ~2s.
    if (this.log) {
      const now = Date.now();
      if (this.paintLogCount < 5 || now - this.lastPaintLog >= 2000) {
        this.paintLogCount += 1;
        this.lastPaintLog = now;
        this.log('canvas paint', {
          glyphsDrawn,
          canvasW: this.canvas.width,
          canvasH: this.canvas.height,
          cols,
          rows,
          cellW: Math.round(cw * 100) / 100,
          cellH: Math.round(ch * 100) / 100,
          viewportY: buffer.viewportY,
          baseY: buffer.baseY,
          isAlt: this.term.buffer.active.type === 'alternate',
        });
      }
    }
  }

  private paintCursor(
    ctx: CanvasRenderingContext2D,
    buffer: Terminal['buffer']['active'],
    cw: number,
    ch: number,
  ): void {
    // Only when scrolled to the bottom and focused (cursorX/Y are viewport-relative).
    if (!this.focused || buffer.viewportY !== buffer.baseY) return;
    const cx = buffer.cursorX;
    const cy = buffer.cursorY;
    if (cx < 0 || cy < 0 || cy >= this.term.rows) return;
    const x = cx * cw;
    const y = cy * ch;
    ctx.fillStyle = this.theme.cursor;
    ctx.fillRect(x, y, cw, ch);
    // Redraw the glyph under the cursor in the background color for contrast.
    const line = buffer.getLine(buffer.viewportY + cy);
    const cell = line?.getCell(cx);
    const chars = cell?.getChars();
    if (chars && chars !== ' ') {
      ctx.fillStyle = this.theme.background;
      ctx.font = `${this.fontSize}px ${this.fontFamily}`;
      ctx.fillText(chars, x, y + ch / 2);
    }
  }

  dispose(): void {
    if (this.disposed) return;
    this.disposed = true;
    window.removeEventListener('resize', this.onWindowResize);
    this.container.removeEventListener('focusin', this.onFocusIn);
    this.container.removeEventListener('focusout', this.onFocusOut);
    this.resizeObserver.disconnect();
    for (const t of this.catchupTimers) clearTimeout(t);
    for (const d of this.disposables) {
      try {
        d.dispose();
      } catch {
        // ignore
      }
    }
    this.canvas.remove();
  }
}

// isCellSelected reports whether an absolute (buffer-row, col) lies within the
// inclusive-start, exclusive-end selection range. Exported for unit testing.
export function isCellSelected(
  selection: { start: { x: number; y: number }; end: { x: number; y: number } } | undefined,
  absRow: number,
  col: number,
): boolean {
  if (!selection) return false;
  const { start, end } = selection;
  if (absRow < start.y || absRow > end.y) return false;
  if (start.y === end.y) return col >= start.x && col < end.x;
  if (absRow === start.y) return col >= start.x;
  if (absRow === end.y) return col < end.x;
  return true;
}
