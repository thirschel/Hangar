import { FitAddon } from '@xterm/addon-fit';
import { Terminal } from '@xterm/xterm';
import { useEffect, useImperativeHandle, useRef, forwardRef } from 'react';
import '@xterm/xterm/css/xterm.css';
import { isAtBottom, normalizeHistory, shouldReconcile } from './termHistory';
import { appHandlesWheel, encodeWheelSgr } from './termWheel';

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
    let lastScrollbackLines = 0;
    let reconcileTimer: number | undefined;
    let reconcileInFlight = false;

    const term = new Terminal({
      cols: 120,
      rows: 30,
      scrollback: 10000, // Allow scrolling back through conversation history
      cursorBlink: true,
      fontFamily: 'Consolas, "Cascadia Mono", "Cascadia Code", monospace',
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

    const reconcileHistory = async (): Promise<void> => {
      if (
        disposed ||
        reconcileInFlight ||
        term.hasSelection() ||
        term.buffer.active.type !== 'normal'
      ) {
        return;
      }
      const atBottom = termIsAtBottom();
      if (atBottom) return;

      const viewportY = term.buffer.active.viewportY;
      reconcileInFlight = true;
      try {
        const history = await window.cs.getHistory(session, true, {
          cols: term.cols,
          rows: term.rows,
        });
        const stillAtBottom = termIsAtBottom();
        if (disposed || term.hasSelection() || stillAtBottom) return;

        const previousScrollbackLines = lastScrollbackLines;
        lastScrollbackLines = history.scrollbackLines;
        if (
          history.altScreen ||
          !history.ansi ||
          !shouldReconcile(previousScrollbackLines, history.scrollbackLines, stillAtBottom)
        ) {
          return;
        }

        term.reset();
        await writeTerm(normalizeHistory(history.ansi));
        if (!disposed) {
          term.scrollToLine(Math.min(viewportY, term.buffer.active.baseY));
        }
      } catch {
        // History refresh is best-effort; live terminal streaming must continue.
      } finally {
        reconcileInFlight = false;
      }
    };

    const scheduleReconcile = (): void => {
      if (reconcileTimer !== undefined) {
        window.clearTimeout(reconcileTimer);
      }
      if (
        disposed ||
        term.hasSelection() ||
        term.buffer.active.type !== 'normal' ||
        termIsAtBottom()
      ) {
        return;
      }
      reconcileTimer = window.setTimeout(() => {
        reconcileTimer = undefined;
        void reconcileHistory();
      }, 150);
    };

    // Wheel handler: scroll the terminal when in normal buffer mode.
    // When apps use the alternate screen buffer (fullscreen TUIs) or enable
    // mouse tracking, wheel events should pass through to the app normally.
    const onWheel = (ev: WheelEvent): void => {
      // Normal (scrollable) buffer only. xterm scrolls its own viewport natively,
      // so we must NOT call term.scrollLines here as well (that double-scrolls).
      // We only refresh host-backed history and prevent the page from scrolling.
      if (term.buffer.active.type === 'normal') {
        scheduleReconcile();
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
    const scrollDisposable = term.onScroll(() => scheduleReconcile());

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
        // "Follow mode": only auto-scroll to bottom when user is already at bottom.
        // This preserves the user's scroll position when reviewing history.
        // viewportY is the buffer line at the top of the viewport; it equals baseY
        // only when scrolled fully to the bottom (and is smaller when scrolled up).
        const wasAtBottom = termIsAtBottom();
        term.write(toBytes(d.chunk));
        if (wasAtBottom) {
          term.scrollToBottom();
        }
      }
    });
    const unsubClosed = window.cs.onClosed((c) => {
      if (c.session === session) term.writeln(`\r\n\x1b[90m${endedLabel}\x1b[0m`);
    });

    const observer = new ResizeObserver(() => resize());
    observer.observe(containerRef.current);

    // Subscribe BEFORE attaching so we catch the host's emulator snapshot. Prime
    // scrollback before attaching so the live screen lands below prior history.
    const connect = async (): Promise<void> => {
      try {
        const history = await window.cs.getHistory(session, false, {
          cols: term.cols,
          rows: term.rows,
        });
        if (!disposed) {
          lastScrollbackLines = history.scrollbackLines;
          if (history.ansi && !history.altScreen) {
            await writeTerm(normalizeHistory(history.ansi));
          }
        }
      } catch {
        // History priming is best-effort; never block the live attach.
      }

      if (disposed) return;
      window.cs
        .attachSession(session, { cols: term.cols, rows: term.rows })
        .then(() => {
          if (disposed) return;
          resize();
          term.focus();
        })
        .catch((error: unknown) => {
          if (disposed) return;
          term.writeln(
            `\r\n\x1b[31m[attach error: ${error instanceof Error ? error.message : String(error)}]\x1b[0m`,
          );
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
      if (reconcileTimer !== undefined) {
        window.clearTimeout(reconcileTimer);
      }
      if (settleRaf1) cancelAnimationFrame(settleRaf1);
      if (settleRaf2) cancelAnimationFrame(settleRaf2);
      el.removeEventListener('contextmenu', onContextMenu);
      el.removeEventListener('wheel', onWheel);
      el.removeEventListener('wheel', onWheelCapture, { capture: true });
      observer.disconnect();
      scrollDisposable.dispose();
      inputDisposable.dispose();
      unsubData();
      unsubClosed();
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
