import { useCallback, useEffect, useRef, useState } from 'react';
import type { WorkspaceInfo } from '../../../main/host-client';

type RunPanelProps = {
  workspace: WorkspaceInfo | null;
};

// Strip ANSI escape sequences (CSI/OSC/single-char) so dev-server output renders
// cleanly in a plain <pre>.
const ANSI =
  // eslint-disable-next-line no-control-regex
  /\x1B(?:[@-Z\\-_]|\[[0-?]*[ -/]*[@-~]|\][^\x07]*(?:\x07|\x1B\\))/g;
const stripAnsi = (s: string): string => s.replace(ANSI, '');

export function RunPanel({ workspace }: RunPanelProps): JSX.Element {
  const [command, setCommand] = useState('');
  const [output, setOutput] = useState('');
  const [error, setError] = useState<string | null>(null);
  const offsetRef = useRef(0);
  const wsIdRef = useRef<string | null>(null);
  const preRef = useRef<HTMLPreElement | null>(null);

  const running = workspace?.running ?? false;
  const previewUrl = workspace?.previewUrl ?? '';
  const id = workspace?.id ?? null;

  const fetchOutput = useCallback(async (workspaceId: string): Promise<void> => {
    try {
      const r = await window.cs.workspaceRunOutput(workspaceId, offsetRef.current);
      if (wsIdRef.current !== workspaceId) return;
      if (r.data) {
        offsetRef.current = r.nextOffset;
        setOutput((o) => o + stripAnsi(r.data));
      }
    } catch {
      // Transient (workspace gone / daemon busy) — ignore; the poll retries.
    }
  }, []);

  // Reset + seed when the selected workspace changes; grab any buffered output.
  useEffect(() => {
    wsIdRef.current = id;
    offsetRef.current = 0;
    setOutput('');
    setError(null);
    setCommand(workspace?.runCommand ?? '');
    if (id) void fetchOutput(id);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [id]);

  // Poll for live output while the run is active.
  useEffect(() => {
    if (!id || !running) return;
    const timer = setInterval(() => void fetchOutput(id), 1200);
    return () => clearInterval(timer);
  }, [id, running, fetchOutput]);

  // Keep the output scrolled to the bottom as it grows.
  useEffect(() => {
    const pre = preRef.current;
    if (pre) pre.scrollTop = pre.scrollHeight;
  }, [output]);

  const start = useCallback(async (): Promise<void> => {
    if (!id || !command.trim()) return;
    setError(null);
    offsetRef.current = 0;
    setOutput('');
    try {
      await window.cs.startRun(id, command.trim());
      void fetchOutput(id);
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    }
  }, [id, command, fetchOutput]);

  const stop = useCallback(async (): Promise<void> => {
    if (!id) return;
    try {
      await window.cs.stopRun(id);
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    }
  }, [id]);

  if (!workspace) {
    return <section className="run-panel run-panel--empty">Select a workspace to run a command.</section>;
  }

  return (
    <section className="run-panel">
      <div className="run-panel__header">
        <span className="run-panel__title">
          Run
          {running && <span className="run-panel__dot" title="running" />}
        </span>
        {previewUrl && (
          <button
            type="button"
            className="run-panel__preview"
            title={previewUrl}
            onClick={() => void window.cs.openExternal(previewUrl)}
          >
            Open preview ↗
          </button>
        )}
      </div>

      <div className="run-panel__controls">
        <input
          className="run-panel__cmd"
          value={command}
          placeholder="npm run dev"
          spellCheck={false}
          disabled={running}
          onChange={(e) => setCommand(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === 'Enter' && !running) void start();
          }}
        />
        {running ? (
          <button type="button" className="run-panel__stop" onClick={() => void stop()}>
            Stop
          </button>
        ) : (
          <button type="button" className="run-panel__start" disabled={!command.trim()} onClick={() => void start()}>
            Start
          </button>
        )}
      </div>

      {error && <div className="run-panel__error">{error}</div>}

      <pre ref={preRef} className="run-panel__output">
        {output || (running ? 'starting…' : 'Not running. Enter a command and press Start.')}
      </pre>
    </section>
  );
}
