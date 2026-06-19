import type { JSX } from 'react';
import { FitAddon } from '@xterm/addon-fit';
import { Terminal } from '@xterm/xterm';
import { useEffect, useImperativeHandle, useRef, forwardRef } from 'react';
import '@xterm/xterm/css/xterm.css';
import { isAtBottom, normalizeHistory } from './termHistory';
import { appHandlesWheel, encodeWheelSgr } from './termWheel';
import { diag } from '../diag';

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
      fontFamily:
        '"CaskaydiaCove Nerd Font", "CaskaydiaMono Nerd Font", "MesloLGM Nerd Font", "MesloLGS NF", "FiraCode Nerd Font", "JetBrainsMono Nerd Font", "Hack Nerd Font", "Cascadia Code NF", "Cascadia Mono NF", Consolas, "Cascadia Mono", "Cascadia Code", monospace',
      fontSize: 13,
      allowProposedApi: true,
      windowsPty: { backend: 'conpty', buildNumber: 26100 },
      theme: {
        background: '#1e1e1e',
        foreground: '#d4d4d4',
        cursor: '#ffffff',
        selectionBackground: '#264f78',
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

    const resize = (): void => {
      try {
        fit.fit();
        window.cs.resize(session, term.cols, term.rows);
      } catch {
        // Fit can throw while the element is detached during startup/teardown.
      }
    };
    refitRef.current = resize;

    const inputDisposable = term.onData((data) => window.cs.sendInput(session, data));
    const unsubData = window.cs.onData((d) => {
      if (d.session === session) {
        const bytes = toBytes(d.chunk);
        dataEvents += 1;
        dataBytes += bytes.byteLength;
        if (!firstDataLogged) {
          firstDataLogged = true;
          diag('TermView first data', { session, bytes: bytes.byteLength });
        }
        // "Follow mode": only auto-scroll to bottom when user is already at bottom.
        // This preserves the user's scroll position when reviewing history.
        // viewportY is the buffer line at the top of the viewport; it equals baseY
        // only when scrolled fully to the bottom (and is smaller when scrolled up).
        const wasAtBottom = termIsAtBottom();
        term.write(bytes, () => {
          if (dataEvents === 1) diag('TermView first write done', { session });
        });
        if (wasAtBottom) {
          term.scrollToBottom();
        }
      } else {
        // A term:data event for another session reached this view — would mean
        // the renderer-side session filter is the reason a pane stays blank.
        diag('TermView data session mismatch', { expected: session, got: d.session }, 'error');
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

    let resizeTimer: ReturnType<typeof setTimeout> | null = null;
    const scheduleResize = (): void => {
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
    // Wait for flex/grid layout, fonts, and xterm cell measurement to settle
    // (two frames) so fit() reflects the real pane BEFORE we prime history and
    // bind the live attach. Otherwise a workspace switch attaches at the default
    // 120x30 and only self-corrects on an OS resize (the bug being fixed).
    settleRaf1 = requestAnimationFrame(() => {
      settleRaf1 = 0;
      settleRaf2 = requestAnimationFrame(() => {
        settleRaf2 = 0;
        if (disposed) return;
        try {
          fit.fit();
        } catch {
          // Element can be detached mid-startup/teardown; connect() still
          // attaches at the current size and the ResizeObserver refit corrects it.
        }
        void connect();
      });
    });

    return () => {
      disposed = true;
      if (settleRaf1) cancelAnimationFrame(settleRaf1);
      if (settleRaf2) cancelAnimationFrame(settleRaf2);
      if (resizeTimer) {
        clearTimeout(resizeTimer);
      }
      el.removeEventListener('contextmenu', onContextMenu);
      el.removeEventListener('wheel', onWheel);
      el.removeEventListener('wheel', onWheelCapture, { capture: true });
      observer.disconnect();
      inputDisposable.dispose();
      unsubData();
      unsubClosed();
      unsubError();
      diag('TermView unmount', { session, dataEvents, dataBytes });
      window.cs.detachSession(session);
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
