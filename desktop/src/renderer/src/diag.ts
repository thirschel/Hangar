// Renderer diagnostics helper. Forwards events and uncaught errors to the main
// process (→ desktop.log, openable via Settings → Diagnostics) because the
// renderer console is otherwise invisible on locked-down machines where DevTools
// is unavailable. Every call is best-effort and never throws.

export function diag(event: string, data?: unknown, level: 'info' | 'error' = 'info'): void {
  try {
    window.cs?.diag?.(event, data, level);
  } catch {
    // Never let diagnostics logging break the app.
  }
}

let installed = false;

// installGlobalDiagnostics wires window.onerror + unhandledrejection so any
// uncaught renderer exception (the prime suspect for a blank pane on a machine
// where DevTools can't be opened) is recorded in desktop.log.
export function installGlobalDiagnostics(): void {
  if (installed) return;
  installed = true;

  window.addEventListener('error', (e: ErrorEvent) => {
    diag(
      'window.onerror',
      {
        message: e.message,
        source: e.filename,
        line: e.lineno,
        col: e.colno,
        stack: e.error instanceof Error ? e.error.stack : undefined,
      },
      'error',
    );
  });

  window.addEventListener('unhandledrejection', (e: PromiseRejectionEvent) => {
    const reason = e.reason;
    diag(
      'unhandledrejection',
      {
        message: reason instanceof Error ? reason.message : String(reason),
        stack: reason instanceof Error ? reason.stack : undefined,
      },
      'error',
    );
  });

  diag('renderer boot', {
    userAgent: navigator.userAgent,
    visibility: document.visibilityState,
  });
}
