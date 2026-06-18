import { forwardRef, useEffect, useImperativeHandle, useRef, useState } from 'react';
import type { WorkspaceInfo } from '../../../main/host-client';
import { TermView, type TermViewHandle } from './TermView';

type ShellTerminalProps = {
  workspace: WorkspaceInfo | null;
};

// ShellTerminal lazily starts a PowerShell session in the workspace's worktree
// (kept alive in the daemon so re-opening re-attaches the same shell) and renders
// it via the shared TermView. Forwards a refit handle for tab-show resizing.
export const ShellTerminal = forwardRef<TermViewHandle, ShellTerminalProps>(function ShellTerminal(
  { workspace },
  ref,
): JSX.Element {
  const [session, setSession] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const inner = useRef<TermViewHandle>(null);

  useImperativeHandle(
    ref,
    () => ({
      refit: () => inner.current?.refit(),
      openFind: () => inner.current?.openFind(),
    }),
    [],
  );

  const wsId = workspace?.id;
  const worktreePath = workspace?.worktreePath;

  useEffect(() => {
    setSession(null);
    setError(null);
    if (!wsId || !worktreePath) return;
    let active = true;
    window.cs
      .ensureShell(wsId, worktreePath, { cols: 120, rows: 30 })
      .then((s) => {
        if (active) setSession(s);
      })
      .catch((e) => {
        if (active) setError(e instanceof Error ? e.message : String(e));
      });
    return () => {
      active = false;
    };
  }, [wsId, worktreePath]);

  if (!workspace) {
    return (
      <div className="agent-terminal agent-terminal--empty">
        <div>Select a workspace to open a shell.</div>
      </div>
    );
  }
  if (error) {
    return (
      <div className="agent-terminal agent-terminal--empty">
        <div>Could not start shell: {error}</div>
      </div>
    );
  }
  if (!session) {
    return (
      <div className="agent-terminal agent-terminal--empty">
        <div>Starting PowerShell…</div>
      </div>
    );
  }
  return <TermView ref={inner} sessionName={session} endedLabel="[shell ended]" />;
});
