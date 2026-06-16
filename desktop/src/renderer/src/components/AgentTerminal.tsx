import { FitAddon } from '@xterm/addon-fit';
import { Terminal } from '@xterm/xterm';
import { useEffect, useRef } from 'react';
import '@xterm/xterm/css/xterm.css';

type AgentTerminalProps = {
  sessionName: string | null;
};

export function AgentTerminal({ sessionName }: AgentTerminalProps): JSX.Element {
  const containerRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!containerRef.current || !sessionName) {
      return;
    }

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
        if (text) window.cs.sendInput(text);
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
        window.cs.resize(term.cols, term.rows);
      } catch {
        // Fit can throw while the element is detached during startup/teardown.
      }
    };

    const inputDisposable = term.onData((data) => window.cs.sendInput(data));
    const unsubData = window.cs.onData((chunk) => term.write(toBytes(chunk)));
    const unsubClosed = window.cs.onClosed(() => term.writeln('\r\n\x1b[90m[agent session ended]\x1b[0m'));

    const observer = new ResizeObserver(() => resize());
    observer.observe(containerRef.current);

    // Subscribe BEFORE attaching so we catch the host's emulator snapshot.
    window.cs
      .attachWorkspace(sessionName, { cols: term.cols, rows: term.rows })
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
      term.dispose();
    };
  }, [sessionName]);

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
}

function toBytes(chunk: Uint8Array): Uint8Array {
  if (chunk instanceof Uint8Array) {
    return chunk;
  }
  return new Uint8Array(chunk);
}
