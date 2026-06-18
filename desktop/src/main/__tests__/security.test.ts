import { describe, expect, it } from 'vitest';
import path from 'node:path';
import {
  assertAuthorizedWorktree,
  canonicalize,
  classifyWindowOpen,
  isAllowedNavigationUrl,
  isStrictlyUnder,
  resolveWithinWorktree,
  resolveWorktreeBase,
} from '../security';

// The desktop app is Windows-only and Desktop CI runs on windows-latest, so these
// tests assume win32 path semantics (matching settings.test.ts).

describe('resolveWorktreeBase', () => {
  const home = 'C:\\Users\\tester';

  it('uses the configured worktree_dir when set', () => {
    expect(resolveWorktreeBase(home, 'D:\\custom\\wt')).toBe(path.resolve('D:\\custom\\wt'));
  });

  it('trims and honors a padded configured dir', () => {
    expect(resolveWorktreeBase(home, '  D:\\custom\\wt  ')).toBe(path.resolve('D:\\custom\\wt'));
  });

  it('falls back to ~/.hangar/worktrees when unset or blank', () => {
    const expected = path.join(home, '.hangar', 'worktrees');
    expect(resolveWorktreeBase(home, undefined)).toBe(expected);
    expect(resolveWorktreeBase(home, '')).toBe(expected);
    expect(resolveWorktreeBase(home, '   ')).toBe(expected);
  });
});

describe('isStrictlyUnder', () => {
  const base = 'C:\\Users\\tester\\.hangar\\worktrees';

  it('accepts a direct child', () => {
    expect(isStrictlyUnder(base, base + '\\thirschel\\repo_abc')).toBe(true);
  });

  it('accepts a deeply nested child', () => {
    expect(isStrictlyUnder(base, base + '\\a\\b\\c')).toBe(true);
  });

  it('rejects the base itself (must be strictly deeper)', () => {
    expect(isStrictlyUnder(base, base)).toBe(false);
  });

  it('rejects a path outside the base', () => {
    expect(isStrictlyUnder(base, 'C:\\Windows\\System32')).toBe(false);
  });

  it('rejects a sibling that shares a name prefix', () => {
    expect(isStrictlyUnder(base, 'C:\\Users\\tester\\.hangar\\worktrees-evil\\x')).toBe(false);
  });

  it('rejects a traversal that escapes the base', () => {
    expect(isStrictlyUnder(base, base + '\\..\\..\\Windows')).toBe(false);
  });

  it('is case-insensitive on Windows', () => {
    expect(isStrictlyUnder(base.toUpperCase(), base + '\\child')).toBe(true);
  });
});

describe('assertAuthorizedWorktree', () => {
  const base = 'C:\\Users\\tester\\.hangar\\worktrees';

  it('does not throw for an authorized worktree', () => {
    expect(() => assertAuthorizedWorktree(base, base + '\\thirschel\\repo')).not.toThrow();
  });

  it('returns the canonical worktree root', () => {
    expect(assertAuthorizedWorktree(base, base + '\\thirschel\\repo')).toBe(
      path.resolve(base + '\\thirschel\\repo'),
    );
  });

  it('throws for a path outside the base', () => {
    expect(() => assertAuthorizedWorktree(base, 'C:\\Windows\\System32')).toThrow(
      /not an authorized workspace/,
    );
  });

  it('throws for an empty path', () => {
    expect(() => assertAuthorizedWorktree(base, '')).toThrow(/not an authorized workspace/);
  });

  it('rejects a junction worktree root that resolves outside the base (canonicalized)', () => {
    // The supplied path is lexically under the base, but realpath resolves the
    // junction to a system directory — it must be rejected.
    const junction = base + '\\escape';
    const realpath = (p: string): string => (p === path.resolve(junction) ? 'C:\\Windows' : p);
    expect(() => assertAuthorizedWorktree(base, junction, realpath)).toThrow(
      /not an authorized workspace/,
    );
  });

  it('accepts a worktree whose real path stays under the base', () => {
    const wt = base + '\\thirschel\\repo';
    const realpath = (p: string): string => p; // identity: resolves to itself
    expect(assertAuthorizedWorktree(base, wt, realpath)).toBe(path.resolve(wt));
  });
});

describe('canonicalize', () => {
  it('resolves to an absolute path and applies realpath', () => {
    const realpath = (p: string): string => p + '\\real';
    expect(canonicalize('C:\\a\\b', realpath)).toBe('C:\\a\\b\\real');
  });

  it('falls back to the lexical absolute path when realpath throws', () => {
    const realpath = (): string => {
      throw Object.assign(new Error('ENOENT'), { code: 'ENOENT' });
    };
    expect(canonicalize('C:\\a\\..\\b', realpath)).toBe(path.resolve('C:\\a\\..\\b'));
  });
});

describe('resolveWithinWorktree', () => {
  const root = 'C:\\Users\\tester\\.hangar\\worktrees\\thirschel\\repo';

  it('resolves a normal relative file inside the worktree', () => {
    expect(resolveWithinWorktree(root, 'src\\index.ts')).toBe(path.resolve(root, 'src\\index.ts'));
  });

  it('rejects lexical traversal out of the worktree', () => {
    expect(() => resolveWithinWorktree(root, '..\\..\\secrets.txt')).toThrow(
      /outside the worktree/,
    );
  });

  it('rejects a symlink inside the worktree that resolves outside it', () => {
    // A malicious cloned repo plants `escape` -> C:\Users\tester\.ssh; the lexical
    // path is contained but realpath resolves the link outside the worktree.
    const realpath = (p: string): string =>
      p === path.resolve(root, 'escape\\id_rsa') ? 'C:\\Users\\tester\\.ssh\\id_rsa' : p;
    expect(() => resolveWithinWorktree(root, 'escape\\id_rsa', realpath)).toThrow(
      /outside the worktree/,
    );
  });

  it('accepts a symlink that resolves to a sibling inside the worktree', () => {
    const realpath = (p: string): string =>
      p === path.resolve(root, 'link\\f') ? path.join(root, 'real', 'f') : p;
    expect(() => resolveWithinWorktree(root, 'link\\f', realpath)).not.toThrow();
  });
});

describe('classifyWindowOpen', () => {
  it('routes http and https to the external browser', () => {
    expect(classifyWindowOpen('http://example.com')).toBe('external');
    expect(classifyWindowOpen('https://example.com/path')).toBe('external');
  });

  it('drops non-web and dangerous schemes', () => {
    expect(classifyWindowOpen('file:///C:/Windows/System32/calc.exe')).toBe('drop');
    expect(classifyWindowOpen('mailto:a@b.com')).toBe('drop');
    expect(classifyWindowOpen('javascript:alert(1)')).toBe('drop');
    expect(classifyWindowOpen('not a url')).toBe('drop');
  });
});

describe('isAllowedNavigationUrl', () => {
  it('allows same-origin navigation for the dev server', () => {
    expect(isAllowedNavigationUrl('http://localhost:5173/', 'http://localhost:5173/index.html')).toBe(true);
  });

  it('blocks a different origin for the dev server', () => {
    expect(isAllowedNavigationUrl('http://localhost:5173/', 'http://evil.example/')).toBe(false);
    expect(isAllowedNavigationUrl('http://localhost:5173/', 'https://localhost:5173/')).toBe(false);
  });

  it('allows navigation within the packaged renderer directory', () => {
    const app = 'file:///C:/Users/tester/app/renderer/index.html';
    expect(isAllowedNavigationUrl(app, 'file:///C:/Users/tester/app/renderer/assets/page.html')).toBe(true);
  });

  it('blocks file navigation outside the renderer directory', () => {
    const app = 'file:///C:/Users/tester/app/renderer/index.html';
    expect(isAllowedNavigationUrl(app, 'file:///C:/Windows/System32/x.html')).toBe(false);
  });

  it('blocks a remote UNC/SMB file host even when the path prefix matches', () => {
    // file://attacker.com/C:/.../renderer/evil.html has a matching pathname prefix
    // but a remote host, which Chromium would load over SMB — must be rejected.
    const app = 'file:///C:/Users/tester/app/renderer/index.html';
    expect(
      isAllowedNavigationUrl(app, 'file://attacker.com/C:/Users/tester/app/renderer/evil.html'),
    ).toBe(false);
  });

  it('blocks an encoded-separator traversal that string-matches the prefix', () => {
    // %2f is not decoded by URL.pathname, so a raw prefix check would pass; resolving
    // via fileURLToPath rejects encoded separators outright.
    const app = 'file:///C:/Users/tester/app/renderer/index.html';
    expect(
      isAllowedNavigationUrl(
        app,
        'file:///C:/Users/tester/app/renderer/..%2f..%2fWindows/System32/x.html',
      ),
    ).toBe(false);
  });

  it('blocks a sibling directory that shares the renderer name prefix', () => {
    const app = 'file:///C:/Users/tester/app/renderer/index.html';
    expect(isAllowedNavigationUrl(app, 'file:///C:/Users/tester/app/renderer-evil/x.html')).toBe(
      false,
    );
  });

  it('blocks a protocol switch', () => {
    const app = 'file:///C:/Users/tester/app/renderer/index.html';
    expect(isAllowedNavigationUrl(app, 'http://localhost:5173/')).toBe(false);
  });

  it('blocks an unparseable target', () => {
    expect(isAllowedNavigationUrl('http://localhost:5173/', 'not a url')).toBe(false);
  });
});
