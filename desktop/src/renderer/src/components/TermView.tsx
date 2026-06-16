import { FitAddon } from '@xterm/addon-fit';
import { Terminal } from '@xterm/xterm';
import { useEffect, useImperativeHandle, useRef, forwardRef } from 'react';
import '@xterm/xterm/css/xterm.css';

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

    const term = new Terminal({
      cols: 120,
      rows: 30,
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

    const copySelection = (): boolean => {
      const sel = term.getSelection();
      if (!sel) return false;
      void navigator.clipboard.writeText(sel);
      return true;
    };
    const paste = (): void => {
      void navigator.clipboard.readText().then((text) => {
        if (text) window.cs.sendInput(session, text);
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
      if (d.session === session) term.write(toBytes(d.chunk));
    });
    const unsubClosed = window.cs.onClosed((c) => {
      if (c.session === session) term.writeln(`\r\n\x1b[90m${endedLabel}\x1b[0m`);
    });

    const observer = new ResizeObserver(() => resize());
    observer.observe(containerRef.current);

    // Subscribe BEFORE attaching so we catch the host's emulator snapshot.
    window.cs
      .attachSession(session, { cols: term.cols, rows: term.rows })
      .then(() => {
        resize();
        term.focus();
      })
      .catch((error: unknown) => {
        term.writeln(`\r\n\x1b[31m[attach error: ${error instanceof Error ? error.message : String(error)}]\x1b[0m`);
      });

    setTimeout(resize, 0);

    return () => {
      el.removeEventListener('contextmenu', onContextMenu);
      observer.disconnect();
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
