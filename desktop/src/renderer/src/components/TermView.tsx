import type { JSX } from 'react';
import { FitAddon } from '@xterm/addon-fit';
import { Terminal } from '@xterm/xterm';
import { useEffect, useImperativeHandle, useRef, forwardRef } from 'react';
import '@xterm/xterm/css/xterm.css';
import { isAtBottom, normalizeHistory } from './termHistory';
import { appHandlesWheel, encodeWheelSgr } from './termWheel';
import { CanvasTermRenderer } from './canvasRenderer';
import { diag } from '../diag';

const TERM_THEME = {
  background: '#1e1e1e',
  foreground: '#d4d4d4',
  cursor: '#ffffff',
  selectionBackground: '#264f78',
};
const TERM_FONT_FAMILY =
  '"CaskaydiaCove Nerd Font", "CaskaydiaMono Nerd Font", "MesloLGM Nerd Font", "MesloLGS NF", "FiraCode Nerd Font", "JetBrainsMono Nerd Font", "Hack Nerd Font", "Cascadia Code NF", "Cascadia Mono NF", Consolas, "Cascadia Mono", "Cascadia Code", monospace';
const TERM_FONT_SIZE = 13;

// Software compositing (RDP/VDI with no GPU) is fetched once and cached. In that
// mode Chromium's software compositor frequently fails to flush xterm's paint, so
// the terminal stays blank until a reflow; the TermView forces reflows below only
// when this is true (no effect on normal GPU machines). This DOM-reflow path is a
// complement to the canvas renderer: it still helps when the DOM renderer is in
// use (terminalRenderer 'dom', or before the canvas renderer is active).
let swCompositing = false;
let swCompositingRequested = false;
function ensureSoftwareCompositingFlag(): void {
  if (swCompositingRequested) return;
  swCompositingRequested = true;
  void window.cs
    .getAppInfo()
    .then((info) => {
      swCompositing = Boolean(info.softwareCompositing);
    })
    .catch(() => {
      /* default false; no forced repaints */
    });
}

type TermViewProps = {
  // The daemon session to stream (agent ws_… or shell sh_…). Null renders nothing.
  sessionName: string | null;
  endedLabel?: string;
};

export type TermViewHandle = {
  // Re-fit the terminal — call when the pane becomes visible (a hidden xterm
  // can't measure itself, so switching tabs needs an explicit refit).
  refit: () => void;
};

// TermView is a reusable xterm bound to one daemon session via the session-scoped
// attach IPC, so the agent and an in-worktree shell can stream concurrently. It
// keeps copy/paste (Ctrl+Shift+C / Ctrl+C-with-selection / Ctrl+Shift+V / right
// click) and the Windows ConPTY backend.
export const TermView = forwardRef<TermViewHandle, TermViewProps>(function TermView(
  { sessionName, endedLabel = '[session ended]' },
  ref,
): JSX.Element {
  const containerRef = useRef<HTMLDivElement>(null);
  const refitRef = useRef<() => void>(() => {});

  useImperativeHandle(ref, () => ({ refit: () => refitRef.current() }), []);

  useEffect(() => {
    if (!containerRef.current || !sessionName) {
      return;
    }
    const session = sessionName;
    let disposed = false;

    const term = new Terminal({
      cols: 120,
      rows: 30,
      scrollback: 10000, // Allow scrolling back through conversation history
      cursorBlink: true,
      fontFamily: TERM_FONT_FAMILY,
      fontSize: TERM_FONT_SIZE,
      allowProposedApi: true,
      windowsPty: { backend: 'conpty', buildNumber: 26100 },
      theme: {
        background: TERM_THEME.background,
        foreground: TERM_THEME.foreground,
        cursor: TERM_THEME.cursor,
        selectionBackground: TERM_THEME.selectionBackground,
      },
    } as ConstructorParameters<typeof Terminal>[0]);
    const fit = new FitAddon();
    term.loadAddon(fit);
    term.open(containerRef.current);
    {
      const r = containerRef.current.getBoundingClientRect();
      diag('TermView mount', {
        session,
        containerW: Math.round(r.width),
        containerH: Math.round(r.height),
        termCols: term.cols,
        termRows: term.rows,
      });
    }

    // Byte accounting for the live stream, reported via diag so a blank pane on a
    // machine without DevTools can be diagnosed from desktop.log: did term:data
    // events arrive at all, did they carry bytes, and did term.write run.
    let dataEvents = 0;
    let dataBytes = 0;
    let firstDataLogged = false;
    // Diagnostics: a short escaped preview of the first agent output chunks (to see
    // whether the agent emits text vs only control/query sequences), and a throttled
    // counter for cross-session data (every pane's onData sees every session's data
    // and filters it — by design, but it can flood the log).
    let previewEventsLogged = 0;
    let mismatchCount = 0;
    let mismatchLastLog = 0;

    // RDP/software-compositing repaint-nudge state. renderInfo gates the
    // renderer-only nudge; isNudging blocks the ResizeObserver from recursing
    // while we briefly change fontSize/cols; nudgeCount bounds automatic fires.
    let renderInfo: Awaited<ReturnType<typeof window.cs.getRenderInfo>> | null = null;
    let isNudging = false;
    let nudgeCount = 0;
    // Canvas-viability self-test: when enabled, an animated 2D <canvas> is overlaid
    // on the pane to decide whether a SINGLE canvas surface presents on this machine
    // (if it animates while xterm is blank, a single-surface renderer / host-side
    // canvas is the fix; if it's also blank, the failure is below the renderer).
    let selfTestRaf = 0;
    let canvasRenderer: CanvasTermRenderer | null = null;
    let selfTestEl: HTMLDivElement | null = null;
    const startRenderSelfTest = (): void => {
      const container = containerRef.current;
      if (disposed || !container || selfTestEl) return;
      const host = document.createElement('div');
      host.style.cssText =
        'position:absolute;top:8px;left:8px;z-index:9999;background:#000;border:1px solid #888;' +
        'padding:6px;font:12px monospace;color:#d4d4d4;pointer-events:none;';
      const label = document.createElement('div');
      label.textContent = 'render self-test — starting…';
      const canvas = document.createElement('canvas');
      canvas.width = 320;
      canvas.height = 80;
      canvas.style.cssText = 'display:block;margin-top:6px;width:320px;height:80px;';
      host.appendChild(label);
      host.appendChild(canvas);
      if (!container.style.position) container.style.position = 'relative';
      container.appendChild(host);
      selfTestEl = host;
      const ctx = canvas.getContext('2d');
      let frames = 0;
      let framesAtLastLog = 0;
      let lastLog = performance.now();
      diag('RenderSelfTest start', { session });
      const loop = (now: number): void => {
        frames += 1;
        if (ctx) {
          ctx.fillStyle = `hsl(${(frames * 3) % 360},70%,40%)`;
          ctx.fillRect(0, 0, canvas.width, canvas.height);
          ctx.fillStyle = '#ffffff';
          ctx.font = '18px monospace';
          ctx.fillText(`canvas frame ${frames}`, 10, 30);
          const x = (frames * 4) % Math.max(1, canvas.width - 44);
          ctx.fillRect(x, canvas.height - 18, 40, 12);
        }
        if (now - lastLog >= 1000) {
          label.textContent = `render self-test • frame ${frames}`;
          diag('RenderSelfTest raf', { session, framesPerSec: frames - framesAtLastLog });
          framesAtLastLog = frames;
          lastLog = now;
        }
        selfTestRaf = requestAnimationFrame(loop);
      };
      selfTestRaf = requestAnimationFrame(loop);
    };
    void window.cs
      .getRenderInfo()
      .then((info) => {
        if (disposed) return;
        renderInfo = info;
        if (info.terminalRenderSelfTest) startRenderSelfTest();
        // Canvas renderer: paint the terminal to one 2D <canvas> instead of xterm's
        // DOM rows (the fix for RDP/no-GPU machines where the DOM layer never
        // presents but a canvas does). xterm still parses/buffers/handles input.
        // 'auto' (default) enables it only under detected software compositing.
        const useCanvas =
          info.terminalRenderer === 'canvas' ||
          (info.terminalRenderer === 'auto' && info.softwareCompositing);
        if (useCanvas && containerRef.current) {
          try {
            canvasRenderer = new CanvasTermRenderer(term, containerRef.current, {
              theme: TERM_THEME,
              fontFamily: TERM_FONT_FAMILY,
              fontSize: TERM_FONT_SIZE,
              // Per-paint diagnostics only when explicitly enabled — the canvas
              // renderer is the default on RDP, so it must not spam desktop.log.
              log: info.terminalDiagnostics ? (event, data) => diag(event, data) : undefined,
            });
            // Paint the buffer already populated before the renderer was built.
            canvasRenderer.requestPaint();
            diag('TermView canvas renderer active', { session });
          } catch (error) {
            diag(
              'TermView canvas renderer failed',
              { session, message: error instanceof Error ? error.message : String(error) },
              'error',
            );
          }
        }
      })
      .catch(() => {
        // Render info is best-effort; the nudge just no-ops without it.
      });

    const termIsAtBottom = (): boolean =>
      isAtBottom(term.buffer.active.viewportY, term.buffer.active.baseY);
    const writeTerm = (data: string | Uint8Array): Promise<void> =>
      new Promise((resolve) => {
        if (disposed) {
          resolve();
          return;
        }
        term.write(data, () => resolve());
      });

    const copySelection = (): boolean => {
      const sel = term.getSelection();
      if (!sel) return false;
      void navigator.clipboard.writeText(sel);
      return true;
    };
    const paste = (): void => {
      void navigator.clipboard.readText().then((text) => {
        // Route through xterm so the text is wrapped for bracketed-paste mode
        // when the app enables it (and so right-click and Ctrl+Shift+V behave
        // identically). term.paste fires onData, which forwards to sendInput.
        if (text) term.paste(text);
      });
    };
    term.attachCustomKeyEventHandler((e) => {
      if (e.type !== 'keydown') return true;
      const key = e.key.toLowerCase();
      if (e.ctrlKey && e.shiftKey && key === 'c') {
        copySelection();
        return false;
      }
      if (e.ctrlKey && e.shiftKey && key === 'v') {
        // Suppress Chromium's built-in "paste and match style"; otherwise it
        // pastes a second copy via xterm's native paste handler.
        e.preventDefault();
        paste();
        return false;
      }
      // Ctrl+C copies when there's a selection (Windows-Terminal convention);
      // with no selection it falls through to the agent as an interrupt.
      if (e.ctrlKey && !e.shiftKey && !e.altKey && key === 'c') {
        if (copySelection()) {
          term.clearSelection();
          return false;
        }
      }
      return true;
    });

    const el = containerRef.current;
    const onContextMenu = (ev: MouseEvent): void => {
      ev.preventDefault();
      paste();
    };
    el.addEventListener('contextmenu', onContextMenu);

    // Wheel handler for the normal (scrollable) buffer: xterm scrolls its own
    // viewport natively and keeps a 10k-line scrollback fed by the live stream,
    // so we only stop the page from scrolling. (Alt-screen / mouse-tracking apps
    // are handled by the separate capture-phase SGR wheel encoder below.)
    const onWheel = (ev: WheelEvent): void => {
      if (term.buffer.active.type === 'normal') {
        ev.preventDefault();
      }
    };
    el.addEventListener('wheel', onWheel, { passive: false });

    // Reliable scroll for apps that drive their own scrolling via mouse tracking
    // (alt-screen TUIs like the copilot agent). xterm forwards wheel→mouse too,
    // but its handler cancels the event even when forwarding fails on momentarily
    // stale measurement/coords, silently dropping the scroll — that is the
    // intermittent "sometimes I can't scroll" the agent window exhibits. We encode
    // an SGR wheel report ourselves in the capture phase (before xterm sees it) so
    // the app always receives it. Normal-buffer apps (no mouse mode) fall through
    // to the native scroll + host-scrollback handling above.
    const onWheelCapture = (ev: WheelEvent): void => {
      if (!appHandlesWheel(term.modes.mouseTrackingMode)) return;
      const rect = el.getBoundingClientRect();
      const fracX = rect.width > 0 ? (ev.clientX - rect.left) / rect.width : 0;
      const fracY = rect.height > 0 ? (ev.clientY - rect.top) / rect.height : 0;
      const seq = encodeWheelSgr(ev.deltaY, fracX, fracY, term.cols, term.rows);
      if (!seq) return;
      window.cs.sendInput(session, seq);
      ev.preventDefault();
      ev.stopPropagation();
    };
    el.addEventListener('wheel', onWheelCapture, { capture: true, passive: false });

    // Track the last cols/rows actually sent to the host so a re-fit that lands on
    // the SAME size doesn't issue a redundant ConPTY resize. This is what keeps the
    // native-window repaint nudge (a 1px, sub-cell window resize) from propagating a
    // PTY resize / SIGWINCH to the agent — its ResizeObserver-driven re-fit nets the
    // same cols/rows, so cs.resize is skipped — while genuine size changes still go
    // through. (Also de-spams the host on same-size refits during tab switches.)
    let lastSentCols = 0;
    let lastSentRows = 0;
    const resize = (): void => {
      try {
        fit.fit();
        if (term.cols !== lastSentCols || term.rows !== lastSentRows) {
          lastSentCols = term.cols;
          lastSentRows = term.rows;
          window.cs.resize(session, term.cols, term.rows);
        }
        // Report the pane rect so the main-process diagnostics probe can capture
        // just the terminal region (isolating it from the React UI).
        const rect = containerRef.current?.getBoundingClientRect();
        if (rect) {
          window.cs.setTerminalRect?.(session, {
            x: rect.x,
            y: rect.y,
            width: rect.width,
            height: rect.height,
          });
        }
      } catch {
        // Fit can throw while the element is detached during startup/teardown.
      }
    };
    refitRef.current = resize;

    // Renderer-only re-raster nudge (RDP/software-compositing): briefly toggle
    // fontSize (or cols) by ±1 across two animation frames, then restore, forcing
    // xterm to re-rasterize without an OS-window resize. Guarded by isNudging so
    // the transient size change does not re-enter the ResizeObserver→fit path and
    // recurse. NEVER calls window.cs.resize (no PTY resize / SIGWINCH). Bounded to
    // 2 automatic fires; `force` (the manual "Force terminal repaint" command)
    // bypasses the gate and the bound. See docs/rdp-blank-terminal.md.
    const nudgeRenderer = (force: boolean): void => {
      if (disposed || isNudging) return;
      const mode = renderInfo?.terminalNudge;
      const eligible =
        !!renderInfo?.softwareCompositing && (mode === 'fontsize' || mode === 'cols');
      if (!force) {
        if (!eligible || nudgeCount >= 2) return;
        nudgeCount += 1;
      }
      const useCols = mode === 'cols';
      isNudging = true;
      const restoreFont = term.options?.fontSize ?? 13;
      const baseCols = term.cols;
      const baseRows = term.rows;
      try {
        if (useCols) term.resize(Math.max(1, baseCols + 1), baseRows);
        else term.options.fontSize = restoreFont + 1;
      } catch {
        // Restore below still runs.
      }
      requestAnimationFrame(() => {
        requestAnimationFrame(() => {
          try {
            if (useCols) term.resize(baseCols, baseRows);
            else term.options.fontSize = restoreFont;
          } catch {
            // Element/term may be tearing down; ignore.
          }
          isNudging = false;
          diag('TermView nudge fired', { session, mode: useCols ? 'cols' : 'fontsize', force });
        });
      });
    };

    ensureSoftwareCompositingFlag();
    // forceReflow forces a synchronous relayout + repaint of the terminal. Under
    // software compositing (RDP) xterm updates the DOM but the compositor may not
    // flush the paint until a reflow (the "resize the pane to make it appear"
    // bug); toggling display off/on within one tick forces that paint without any
    // visible flicker (the browser never paints the intermediate state). No-op on
    // GPU machines. Coalesced via scheduleReflow so streamed output is cheap.
    let reflowTimer: ReturnType<typeof setTimeout> | null = null;
    const forceReflow = (): void => {
      if (!swCompositing) return;
      try {
        term.refresh(0, Math.max(0, term.rows - 1));
      } catch {
        // term may be disposed mid-teardown.
      }
      const el = containerRef.current;
      if (el) {
        const prev = el.style.display;
        el.style.display = 'none';
        void el.offsetHeight; // read forces a synchronous reflow
        el.style.display = prev;
      }
    };
    const scheduleReflow = (): void => {
      if (!swCompositing || reflowTimer) return;
      reflowTimer = setTimeout(() => {
        reflowTimer = null;
        forceReflow();
      }, 60);
    };

    const inputDisposable = term.onData((data) => window.cs.sendInput(session, data));
    const unsubData = window.cs.onData((d) => {
      if (d.session === session) {
        const bytes = toBytes(d.chunk);
        dataEvents += 1;
        dataBytes += bytes.byteLength;
        if (!firstDataLogged) {
          firstDataLogged = true;
          diag('TermView first data', { session, bytes: bytes.byteLength });
          nudgeRenderer(false);
        }
        // Log an escaped preview of the first few chunks (diagnostics only) so a
        // blank pane can be diagnosed: if the agent only emits control/query
        // sequences (and waits), the buffer stays empty and nothing renders.
        if (renderInfo?.terminalDiagnostics && previewEventsLogged < 3) {
          previewEventsLogged += 1;
          diag('TermView data preview', {
            session,
            bytes: bytes.byteLength,
            preview: escapeBytesPreview(bytes, 200),
          });
        }
        // "Follow mode": only auto-scroll to bottom when user is already at bottom.
        // This preserves the user's scroll position when reviewing history.
        // viewportY is the buffer line at the top of the viewport; it equals baseY
        // only when scrolled fully to the bottom (and is smaller when scrolled up).
        const wasAtBottom = termIsAtBottom();
        term.write(bytes, () => {
          if (dataEvents === 1) diag('TermView first write done', { session });
          // Repaint the canvas after xterm has parsed the bytes into its buffer
          // (guarantees the canvas reflects new output even if onRender is missed).
          canvasRenderer?.requestPaint();
        });
        if (wasAtBottom) {
          term.scrollToBottom();
        }
        scheduleReflow();
      } else {
        // A term:data event for another session reached this view. This is expected
        // (each pane's onData sees all sessions and filters), so it is NOT an error;
        // throttle a count so it can't flood the log.
        mismatchCount += 1;
        const now = Date.now();
        if (now - mismatchLastLog >= 2000) {
          diag('TermView data session mismatch (filtered)', {
            expected: session,
            count: mismatchCount,
          });
          mismatchLastLog = now;
        }
      }
    });
    const unsubClosed = window.cs.onClosed((c) => {
      if (c.session === session) {
        diag('TermView closed', { session, exitCode: c.exitCode, totalBytes: dataBytes });
        const label =
          typeof c.exitCode === 'number' ? `${endedLabel} (exit ${c.exitCode})` : endedLabel;
        term.writeln(`\r\n\x1b[90m${label}\x1b[0m`);
      }
    });
    const unsubError = window.cs.onError((e) => {
      if (e.session === session) {
        diag('TermView stream error', { session, message: e.message }, 'error');
        term.writeln(`\r\n\x1b[31m[stream error: ${e.message}]\x1b[0m`);
      }
    });
    // Manual "Force terminal repaint" command (Settings → Diagnostics): run the
    // renderer-only nudge regardless of mode/compositing (the user asked for it).
    const unsubNudge = window.cs.onTerminalNudge(() => nudgeRenderer(true));

    let resizeTimer: ReturnType<typeof setTimeout> | null = null;
    const scheduleResize = (): void => {
      // Ignore the transient size change our own nudge causes (prevents the
      // ResizeObserver→fit→nudge re-entrancy loop).
      if (isNudging) return;
      if (resizeTimer) {
        clearTimeout(resizeTimer);
      }
      // Coalesce resize bursts to avoid per-frame fit jitter during the dock slide animation.
      resizeTimer = setTimeout(() => {
        resizeTimer = null;
        resize();
      }, 120);
    };
    const observer = new ResizeObserver(() => scheduleResize());
    observer.observe(containerRef.current);

    // Subscribe BEFORE attaching so we catch the host's emulator snapshot. Prime
    // scrollback before attaching so the live screen lands below prior history.
    const connect = async (): Promise<void> => {
      try {
        const history = await window.cs.getHistory(session, false, {
          cols: term.cols,
          rows: term.rows,
        });
        if (!disposed && history.ansi && !history.altScreen) {
          await writeTerm(normalizeHistory(history.ansi));
        }
      } catch {
        // History priming is best-effort; never block the live attach.
      }

      if (disposed) return;
      window.cs
        .attachSession(session, { cols: term.cols, rows: term.rows })
        .then((attached) => {
          if (disposed) return;
          const r = containerRef.current?.getBoundingClientRect();
          diag('TermView attached', {
            session,
            alive: attached?.alive,
            exitCode: attached?.exitCode,
            ok: attached?.ok,
            containerW: r ? Math.round(r.width) : undefined,
            containerH: r ? Math.round(r.height) : undefined,
            termCols: term.cols,
            termRows: term.rows,
          });
          if (attached?.alive === false) {
            term.writeln(
              `\r\n\x1b[33m[agent process exited (code ${attached.exitCode ?? '?'}) — see host.log via Settings → Diagnostics]\x1b[0m`,
            );
          }
          resize();
          term.focus();
          // Initial-snapshot repaint nudge (no-op unless software compositing +
          // a renderer-only nudge mode). The native-window nudge fires in main.
          nudgeRenderer(false);
          // DOM-renderer fallback for software compositing: paint the initial
          // snapshot without a manual resize. Spaced because xterm's render lands
          // a frame or two after write. No-op on GPU machines / canvas renderer.
          if (swCompositing) {
            for (const delay of [0, 120, 400, 1000]) {
              const t = setTimeout(() => {
                if (!disposed) forceReflow();
              }, delay);
              t.unref?.();
            }
          }
        })
        .catch((error: unknown) => {
          if (disposed) return;
          const message = error instanceof Error ? error.message : String(error);
          diag('TermView attach error', { session, message }, 'error');
          term.writeln(`\r\n\x1b[31m[attach error: ${message}]\x1b[0m`);
        });
    };
    let settleRaf1 = 0;
    let settleRaf2 = 0;
    const twoFrames = (): Promise<void> =>
      new Promise<void>((resolve) => {
        settleRaf1 = requestAnimationFrame(() => {
          settleRaf1 = 0;
          settleRaf2 = requestAnimationFrame(() => {
            settleRaf2 = 0;
            resolve();
          });
        });
      });
    // Wait for fonts to load AND for flex/grid layout + xterm cell measurement to
    // settle (two frames) so fit() reflects the real pane BEFORE we prime history
    // and bind the live attach. Without awaiting document.fonts.ready, the Nerd
    // Font may not be loaded at first paint and cells can measure 0×0 until a later
    // refit (one cause of the blank-pane bug). Otherwise a workspace switch
    // attaches at the default 120x30 and only self-corrects on an OS resize.
    const settle = async (): Promise<void> => {
      try {
        if (document.fonts?.ready) await document.fonts.ready;
      } catch {
        // Fonts API unavailable or rejected; proceed with measurement anyway.
      }
      if (disposed) return;
      await twoFrames();
      if (disposed) return;
      try {
        fit.fit();
      } catch {
        // Element can be detached mid-startup/teardown; connect() still attaches
        // at the current size and the ResizeObserver refit corrects it.
      }
      void connect();
    };
    void settle();

    return () => {
      disposed = true;
      if (settleRaf1) cancelAnimationFrame(settleRaf1);
      if (settleRaf2) cancelAnimationFrame(settleRaf2);
      if (selfTestRaf) cancelAnimationFrame(selfTestRaf);
      if (selfTestEl) {
        selfTestEl.remove();
        selfTestEl = null;
      }
      if (resizeTimer) {
        clearTimeout(resizeTimer);
      }
      if (reflowTimer) {
        clearTimeout(reflowTimer);
      }
      el.removeEventListener('contextmenu', onContextMenu);
      el.removeEventListener('wheel', onWheel);
      el.removeEventListener('wheel', onWheelCapture, { capture: true });
      observer.disconnect();
      inputDisposable.dispose();
      unsubData();
      unsubClosed();
      unsubError();
      unsubNudge();
      diag('TermView unmount', { session, dataEvents, dataBytes });
      window.cs.detachSession(session);
      if (canvasRenderer) {
        canvasRenderer.dispose();
        canvasRenderer = null;
      }
      term.dispose();
      refitRef.current = () => {};
    };
  }, [sessionName, endedLabel]);

  if (!sessionName) {
    return (
      <div className="agent-terminal agent-terminal--empty">
        <div>
          <div className="agent-terminal__empty-title">No workspace selected</div>
          <p>Create a workspace or pick one on the left to start an agent.</p>
        </div>
      </div>
    );
  }
  return <div ref={containerRef} className="agent-terminal" />;
});

function toBytes(chunk: Uint8Array): Uint8Array {
  if (chunk instanceof Uint8Array) {
    return chunk;
  }
  return new Uint8Array(chunk);
}

// escapeBytesPreview renders up to `max` bytes as a human-readable string for
// desktop.log: printable ASCII as-is, ESC as \e, other control/high bytes as \xHH.
// Lets us see whether the agent emitted text or only control/query sequences.
function escapeBytesPreview(bytes: Uint8Array, max: number): string {
  const n = Math.min(bytes.byteLength, max);
  let out = '';
  for (let i = 0; i < n; i++) {
    const b = bytes[i];
    if (b === 0x1b) out += '\\e';
    else if (b >= 0x20 && b <= 0x7e) out += String.fromCharCode(b);
    else if (b === 0x0a) out += '\\n';
    else if (b === 0x0d) out += '\\r';
    else if (b === 0x09) out += '\\t';
    else out += '\\x' + b.toString(16).padStart(2, '0');
  }
  if (bytes.byteLength > max) out += '…';
  return out;
}

