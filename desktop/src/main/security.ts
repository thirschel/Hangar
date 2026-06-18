// Pure security helpers for the Electron main process (HARDEN-13 / F-16, F-23).
//
// These functions are deliberately free of any electron / fs / network imports so
// they can be unit-tested in isolation, and so the trust decisions they encode are
// easy to audit. index.ts wires them into the BrowserWindow guards and the
// renderer-facing filesystem/shell IPC handlers.

import path from 'node:path';
import { fileURLToPath } from 'node:url';

const isWindows = process.platform === 'win32';

// A filesystem path canonicaliser (e.g. fs.realpathSync.native). Injected rather
// than imported so this module stays pure and unit-testable with a fake.
export type RealpathFn = (p: string) => string;

const identityRealpath: RealpathFn = (p) => p;

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

// canonicalize resolves p to an absolute path and then, best-effort, through the
// filesystem (realpath) so symlinks, Windows junctions and 8.3 short names are
// collapsed to their real target. It mirrors the Go daemon's canonicalizePath
// (session/git/pathsafe.go): if realpath fails (e.g. the path does not exist yet)
// it falls back to the lexical absolute path, exactly like EvalSymlinks-on-error.
export function canonicalize(p: string, realpath: RealpathFn = identityRealpath): string {
  const abs = path.resolve(p);
  try {
    return realpath(abs);
  } catch {
    return abs;
  }
}

// assertAuthorizedWorktree throws unless worktreePath is a managed worktree (a path
// strictly under base, after both are canonicalised). The renderer supplies
// worktreePath to the filesystem/shell IPC handlers; without this gate a compromised
// renderer could point it at any directory (arbitrary file read, or a shell spawned
// in an arbitrary cwd) — F-23. Canonicalising defeats a symlink/junction inside the
// worktrees root that lexically appears contained but resolves elsewhere. Returns
// the canonical worktree root for the caller to resolve relative paths against.
export function assertAuthorizedWorktree(
  base: string,
  worktreePath: string,
  realpath: RealpathFn = identityRealpath,
): string {
  if (typeof worktreePath !== 'string' || worktreePath.length === 0) {
    throw new Error('worktreePath is not an authorized workspace');
  }
  const canonicalBase = canonicalize(base, realpath);
  const canonicalWorktree = canonicalize(worktreePath, realpath);
  if (!isStrictlyUnder(canonicalBase, canonicalWorktree)) {
    throw new Error('worktreePath is not an authorized workspace');
  }
  return canonicalWorktree;
}

// resolveWithinWorktree resolves a renderer-supplied relative path against an
// already-canonical worktree root and returns the absolute target, rejecting both
// lexical traversal (../) and symlink/junction escape (a link inside the worktree —
// e.g. planted by a malicious cloned repo — that resolves outside it). The canonical
// containment re-check is what closes the F-23 read-outside-the-worktree vector.
export function resolveWithinWorktree(
  canonicalRoot: string,
  rel: string,
  realpath: RealpathFn = identityRealpath,
): string {
  const target = path.resolve(canonicalRoot, rel || '.');
  if (target !== canonicalRoot && !target.startsWith(canonicalRoot + path.sep)) {
    throw new Error('path is outside the worktree');
  }
  const canonicalTarget = canonicalize(target, realpath);
  if (canonicalTarget !== canonicalRoot && !isStrictlyUnder(canonicalRoot, canonicalTarget)) {
    throw new Error('path is outside the worktree');
  }
  return target;
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
    // file: URLs must be local. A non-empty host means a UNC/SMB target
    // (file://server/share) that Chromium would load from a remote machine, so
    // require the host to match the app's (empty for a normal local file: URL).
    if (target.hostname !== appU.hostname) return false;
    // Resolve both to real filesystem paths and reuse the worktree containment
    // primitive: the target must be a file strictly inside the renderer's own
    // directory. fileURLToPath decodes percent-escapes and, crucially, throws on
    // encoded path separators (%2f / %5c), so an encoded-traversal target that
    // would slip past a raw pathname-prefix comparison is rejected here.
    let appPath: string;
    let targetPath: string;
    try {
      appPath = fileURLToPath(appU);
      targetPath = fileURLToPath(target);
    } catch {
      return false;
    }
    return isStrictlyUnder(path.dirname(appPath), targetPath);
  }
  // http/https dev server: same origin only (permits Vite HMR reloads).
  return appU.origin === target.origin;
}
