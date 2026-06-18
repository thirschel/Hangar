// Pure security helpers for the Electron main process (HARDEN-13 / F-16, F-23).
//
// These functions are deliberately free of any electron / fs / network imports so
// they can be unit-tested in isolation, and so the trust decisions they encode are
// easy to audit. index.ts wires them into the BrowserWindow guards and the
// renderer-facing filesystem/shell IPC handlers.

import path from 'node:path';

const isWindows = process.platform === 'win32';

function segEqual(a: string, b: string): boolean {
  return isWindows ? a.toLowerCase() === b.toLowerCase() : a === b;
}

function segments(p: string): string[] {
  // path.resolve makes the path absolute and collapses '.', '..' and mixed
  // separators; splitting on both separators then dropping empties yields the
  // canonical, comparable path segments (including the Windows drive, e.g. 'C:').
  return path
    .resolve(p)
    .split(/[\\/]+/)
    .filter((s) => s.length > 0);
}

// resolveWorktreeBase mirrors the Go core daemon's getWorktreeDirectory(): use the
// configured worktree_dir when set, otherwise the default ~/.hangar/worktrees. The
// result is the single directory every managed worktree must live under.
export function resolveWorktreeBase(homedir: string, configuredDir: string | undefined): string {
  const configured = (configuredDir ?? '').trim();
  if (configured) return path.resolve(configured);
  return path.join(homedir, '.hangar', 'worktrees');
}

// isStrictlyUnder reports whether target resolves to a path strictly *inside* base
// (deeper than, and sharing every segment of, base). It mirrors the Go-side
// containsCanonical containment check (session/git/pathsafe.go) and is
// case-insensitive on Windows. Equal paths return false — a worktree is always a
// child of the worktrees root, never the root itself.
export function isStrictlyUnder(base: string, target: string): boolean {
  const b = segments(base);
  const t = segments(target);
  if (t.length <= b.length) return false;
  for (let i = 0; i < b.length; i++) {
    if (!segEqual(b[i], t[i])) return false;
  }
  return true;
}

// assertAuthorizedWorktree throws unless worktreePath is a managed worktree (a path
// strictly under base). The renderer supplies worktreePath to the filesystem/shell
// IPC handlers; without this gate a compromised renderer could point it at any
// directory (arbitrary file read, or a shell spawned in an arbitrary cwd) — F-23.
export function assertAuthorizedWorktree(base: string, worktreePath: string): void {
  if (typeof worktreePath !== 'string' || worktreePath.length === 0 || !isStrictlyUnder(base, worktreePath)) {
    throw new Error('worktreePath is not an authorized workspace');
  }
}

// classifyWindowOpen decides what to do with a renderer window-open request: only
// http(s) URLs may be handed to the OS browser; anything else is dropped. The
// window itself is always denied by the caller (setWindowOpenHandler) so the
// renderer can never spawn an in-app Electron window.
export function classifyWindowOpen(url: string): 'external' | 'drop' {
  try {
    const u = new URL(url);
    return u.protocol === 'http:' || u.protocol === 'https:' ? 'external' : 'drop';
  } catch {
    return 'drop';
  }
}

// isAllowedNavigationUrl reports whether a top-level navigation away from the app's
// own document should be permitted. Legitimate navigations only ever target the app
// itself (same http(s) origin for the dev server, or a file within the packaged
// renderer directory); everything else is blocked (F-16) so injected content cannot
// move the main window off-origin.
export function isAllowedNavigationUrl(appUrl: string, targetUrl: string): boolean {
  let appU: URL;
  let target: URL;
  try {
    appU = new URL(appUrl);
    target = new URL(targetUrl);
  } catch {
    return false;
  }
  if (appU.protocol !== target.protocol) return false;
  if (appU.protocol === 'file:') {
    // Allow only navigation within the renderer's own directory. file: URLs have
    // a "null" origin, so compare the directory prefix of the pathname instead.
    const dir = appU.pathname.slice(0, appU.pathname.lastIndexOf('/') + 1);
    if (dir.length === 0) return false;
    const a = isWindows ? dir.toLowerCase() : dir;
    const t = isWindows ? target.pathname.toLowerCase() : target.pathname;
    return t.startsWith(a);
  }
  // http/https dev server: same origin only (permits Vite HMR reloads).
  return appU.origin === target.origin;
}
