import type { Terminal } from '@xterm/xterm';
import { diag } from '../diag';

// Paint diagnostics for the RDP blank-terminal investigation (Phase 0). Enabled
// only when main reports paintDiag (HANGAR_PAINT_DIAG / paintDiagnostics setting),
// so production users are unaffected. Everything is best-effort and never throws.
//
// What it answers — H1 (native-window present/occlusion) vs H2 (in-renderer
// raster) — using three decisive signals, all logged to desktop.log:
//   1. LIVENESS: an always-animating rAF/canvas element whose frame counter is
//      logged. If it FREEZES while the terminal is blank, the whole-window present
//      is paused (H1). If it keeps moving, presents run and only the terminal
//      surface is affected (still a native/surface issue, not an in-renderer nudge).
//   2. CAPTURE: main captures the terminal's on-screen rect (cs:paint-capture). If
//      it shows non-background pixels while the user sees blank, the page rastered
//      but was never presented (H1). If uniform background, raster never ran (H2).
//   3. MATRIX: at a checkpoint, run term.refresh / same-size fit / term.resize(±1)
//      / a NATIVE window-bounds nudge, capturing before/after each, to confirm the
//      native present is the lever (the only known-good action is an OS resize).
// Plus a DOM/measurement burst (rows, cell metrics, computed styles, onRender
// count) to exclude H3 (0×0 cells / fonts).

type CoreWithRender = {
  _core?: {
    _renderService?: {
      dimensions?: {
        css?: { cell?: { width?: number; height?: number } };
        device?: { cell?: { width?: number; height?: number } };
      };
    };
  };
};

export type PaintDiagHandle = {
  // Call when the first stream bytes have been written to the terminal.
  onFirstWrite: () => void;
  dispose: () => void;
};

function rectOf(el: HTMLElement): { x: number; y: number; width: number; height: number } {
  const r = el.getBoundingClientRect();
  return { x: Math.round(r.left), y: Math.round(r.top), width: Math.round(r.width), height: Math.round(r.height) };
}

function cellMetrics(term: Terminal): { cssW?: number; cssH?: number; devW?: number; devH?: number } {
  try {
    const dims = (term as unknown as CoreWithRender)._core?._renderService?.dimensions;
    return {
      cssW: dims?.css?.cell?.width,
      cssH: dims?.css?.cell?.height,
      devW: dims?.device?.cell?.width,
      devH: dims?.device?.cell?.height,
    };
  } catch {
    return {};
  }
}

export function installPaintDiagnostics(opts: {
  term: Terminal;
  container: HTMLElement;
  session: string;
  runFit: () => void;
}): PaintDiagHandle {
  const { term, container, session, runFit } = opts;
  const timers: ReturnType<typeof setTimeout>[] = [];
  let raf = 0;
  let renderCount = 0;
  let renderDisposable: { dispose: () => void } | null = null;
  let disposed = false;
  let firstWriteDone = false;

  try {
    renderDisposable = term.onRender(() => {
      renderCount += 1;
    });
  } catch {
    renderDisposable = null;
  }

  // 1. LIVENESS element: a small fixed canvas in the top-right, redrawn every rAF.
  // The frame counter advancing proves the compositor is still issuing frames; a
  // stall while the terminal is blank pins the fault at the window present (H1).
  const canvas = document.createElement('canvas');
  canvas.width = 16;
  canvas.height = 16;
  canvas.setAttribute('data-paint-diag', 'liveness');
  canvas.style.cssText =
    'position:fixed;top:2px;right:2px;width:16px;height:16px;z-index:99999;pointer-events:none;opacity:0.6;border:1px solid #0f0';
  let frames = 0;
  let canvasOk = false;
  let ctx: CanvasRenderingContext2D | null = null;
  try {
    document.body.appendChild(canvas);
    ctx = canvas.getContext('2d');
    canvasOk = !!ctx;
  } catch {
    canvasOk = false;
  }
  let lastSampleFrames = 0;
  let lastSampleAt = performance.now();
  const tick = (): void => {
    if (disposed) return;
    frames += 1;
    if (ctx) {
      const phase = frames % 16;
      ctx.fillStyle = '#1e1e1e';
      ctx.fillRect(0, 0, 16, 16);
      ctx.fillStyle = '#00ff66';
      ctx.fillRect(phase, phase, 4, 4);
    }
    raf = requestAnimationFrame(tick);
  };
  raf = requestAnimationFrame(tick);

  const sampleLiveness = (label: string): void => {
    const now = performance.now();
    const dt = now - lastSampleAt;
    const df = frames - lastSampleFrames;
    const fps = dt > 0 ? Math.round((df / dt) * 1000) : 0;
    diag('paint-diag liveness', {
      session,
      label,
      frames,
      framesDelta: df,
      ms: Math.round(dt),
      fps,
      canvasOk,
      visibility: document.visibilityState,
    });
    lastSampleFrames = frames;
    lastSampleAt = now;
  };

  // 2 + extra: DOM/measurement burst — excludes H3 (no rows / 0×0 cells / hidden).
  const domBurst = (label: string): void => {
    try {
      const rowsEl = container.querySelector('.xterm-rows') as HTMLElement | null;
      const screenEl = container.querySelector('.xterm-screen') as HTMLElement | null;
      const firstRow = rowsEl?.children?.[0] as HTMLElement | null;
      const cs = screenEl ? getComputedStyle(screenEl) : null;
      diag('paint-diag dom', {
        session,
        label,
        cols: term.cols,
        rows: term.rows,
        rowEls: rowsEl?.children?.length ?? 0,
        firstRowLen: firstRow?.textContent?.length ?? 0,
        container: rectOf(container),
        screenW: screenEl ? Math.round(screenEl.getBoundingClientRect().width) : 0,
        screenH: screenEl ? Math.round(screenEl.getBoundingClientRect().height) : 0,
        display: cs?.display,
        visibility: cs?.visibility,
        opacity: cs?.opacity,
        renderCount,
        ...cellMetrics(term),
      });
    } catch (error) {
      diag('paint-diag dom error', { session, label, message: String(error) }, 'error');
    }
  };

  const capture = async (label: string): Promise<void> => {
    try {
      await window.cs.paintCapture(`${session}:${label}`, rectOf(container));
    } catch {
      /* best-effort */
    }
  };

  const at = (ms: number, fn: () => void): void => {
    const t = setTimeout(fn, ms);
    t.unref?.();
    timers.push(t);
  };

  // 3. Paint-mechanism matrix: capture, then each candidate repaint mechanism in
  // increasing nativeness, capturing after each. The decisive comparison is the
  // native window-bounds nudge vs the in-renderer ones.
  const runMatrix = async (): Promise<void> => {
    if (disposed) return;
    sampleLiveness('matrix:start');
    domBurst('matrix:start');
    await capture('matrix:before');
    try {
      term.refresh(0, Math.max(0, term.rows - 1));
    } catch {
      /* disposed */
    }
    await capture('matrix:after-refresh');
    try {
      runFit();
    } catch {
      /* detached */
    }
    await capture('matrix:after-fit');
    try {
      term.resize(Math.max(2, term.cols - 1), term.rows);
      term.resize(term.cols + 1, term.rows);
    } catch {
      /* disposed */
    }
    await capture('matrix:after-resize');
    try {
      await window.cs.paintNudgeNative(`${session}:matrix`);
    } catch {
      /* best-effort */
    }
    // Give the native nudge a couple frames to present before sampling.
    at(80, () => {
      void capture('matrix:after-native-nudge');
      sampleLiveness('matrix:end');
      domBurst('matrix:end');
    });
  };

  diag('paint-diag installed', { session, canvasOk });
  domBurst('mount');
  void capture('mount');
  // Liveness/DOM/capture checkpoints; the matrix runs once layout has settled.
  at(250, () => {
    sampleLiveness('+250ms');
    domBurst('+250ms');
    void capture('+250ms');
  });
  at(1000, () => {
    sampleLiveness('+1s');
    domBurst('+1s');
    void capture('+1s');
  });
  at(1600, () => {
    void runMatrix();
  });
  at(4000, () => sampleLiveness('+4s'));

  return {
    onFirstWrite: (): void => {
      if (firstWriteDone || disposed) return;
      firstWriteDone = true;
      sampleLiveness('first-write');
      domBurst('first-write');
      void capture('first-write');
    },
    dispose: (): void => {
      disposed = true;
      if (raf) cancelAnimationFrame(raf);
      for (const t of timers) clearTimeout(t);
      renderDisposable?.dispose();
      try {
        canvas.remove();
      } catch {
        /* already gone */
      }
    },
  };
}
