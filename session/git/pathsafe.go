package git

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
)

// shaRe matches a 7–40 hex-character git SHA (short or full).
var shaRe = regexp.MustCompile(`^[0-9a-fA-F]{7,40}$`)

// ErrUnsafeSHA is returned when a BaseCommitSHA fails validation.
var ErrUnsafeSHA = errors.New("unsafe base commit SHA")

// ErrUnsafeWorktreePath is returned when a worktree path is not contained
// within the managed worktrees directory.
var ErrUnsafeWorktreePath = errors.New("worktree path is outside the managed worktrees directory")

// ValidateSHA returns nil iff sha is a 7–40 hex-character string. It does NOT
// verify that the SHA exists in a repo — that is git's job. Its sole purpose is
// to reject argument-injection strings such as "--output=C:\evil" or ".." before
// they are passed as the <commit> argument to `git diff` (F-08). Git honours
// long options that appear before the commit slot, and there is no position to
// insert a "--" end-of-options marker that protects the commit argument, so a
// strict format check at the trust boundary is the only robust defense.
func ValidateSHA(sha string) error {
	if sha == "" {
		return fmt.Errorf("%w: empty SHA", ErrUnsafeSHA)
	}
	if !shaRe.MatchString(sha) {
		return fmt.Errorf("%w: %q does not match [0-9a-fA-F]{7,40}", ErrUnsafeSHA, sha)
	}
	return nil
}

// stripExtendedLengthPrefix removes a Windows extended-length path prefix
// (`\\?\`), normalising `\\?\UNC\server\share` back to `\\server\share` so that
// extended and non-extended spellings of the same path compare equal. It is a
// no-op for paths that do not carry the prefix (and therefore for every POSIX
// path).
func stripExtendedLengthPrefix(p string) string {
	if strings.HasPrefix(p, `\\?\UNC\`) {
		return `\\` + p[len(`\\?\UNC\`):]
	}
	if strings.HasPrefix(p, `\\?\`) {
		return p[len(`\\?\`):]
	}
	return p
}

// canonicalizePath resolves p to an absolute, symlink-free, prefix-normalised
// form suitable for segment comparison. For paths that exist on disk it follows
// symlinks (and, on Windows, expands 8.3 short names and junctions) via
// filepath.EvalSymlinks; for not-yet-created paths (new worktrees) it falls back
// to filepath.Abs. The extended-length (`\\?\`) prefix is stripped from both the
// input and the EvalSymlinks output so spellings compare consistently.
func canonicalizePath(p string) (string, error) {
	if strings.TrimSpace(p) == "" {
		return "", fmt.Errorf("empty path")
	}
	abs, err := filepath.Abs(stripExtendedLengthPrefix(p))
	if err != nil {
		return "", err
	}
	if resolved, evalErr := filepath.EvalSymlinks(abs); evalErr == nil {
		abs = resolved
	}
	abs = stripExtendedLengthPrefix(abs)
	return filepath.Clean(abs), nil
}

// pathSegEqual compares a single path segment (or a volume name) honouring the
// host filesystem's case sensitivity: case-insensitive on Windows, exact
// elsewhere.
func pathSegEqual(a, b string) bool {
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

// pathSegments splits a cleaned path into its component segments, excluding the
// volume name (drive letter or UNC `\\server\share`). It splits on both
// separators so the result is independent of how the path was spelled.
func pathSegments(p string) []string {
	rest := p[len(filepath.VolumeName(p)):]
	rest = strings.TrimLeft(rest, `\/`)
	if rest == "" {
		return nil
	}
	return strings.FieldsFunc(rest, func(r rune) bool { return r == '\\' || r == '/' })
}

// containsCanonical reports whether the already-canonicalised target is a strict
// descendant of the already-canonicalised base. It compares the volume name and
// then each leading path segment individually, rather than using a string-prefix
// test. Segment comparison is what defeats the classic prefix false positives:
//   - `C:\wt` must NOT contain the sibling `C:\wt-evil` (a prefix test would
//     wrongly accept it).
//   - `<base>\..foo` is a legitimate child named "..foo" and is contained, even
//     though it begins with "..".
//
// A target equal to base (zero extra segments) is treated as NOT contained, so
// callers never delete the managed root itself.
func containsCanonical(base, target string) bool {
	if !pathSegEqual(filepath.VolumeName(base), filepath.VolumeName(target)) {
		return false
	}
	baseSegs := pathSegments(base)
	targetSegs := pathSegments(target)
	if len(targetSegs) <= len(baseSegs) {
		return false
	}
	for i := range baseSegs {
		if !pathSegEqual(baseSegs[i], targetSegs[i]) {
			return false
		}
	}
	return true
}

// IsUnderDir reports whether target resolves to a path strictly under base.
// Both paths are canonicalised (absolute, symlinks/8.3-short-names resolved,
// `\\?\`/UNC prefixes normalised) before a volume-aware, segment-by-segment
// comparison. It returns false (not an error) when target is simply outside
// base; it returns an error only when a path cannot be resolved at all.
//
// The comparison is case-insensitive on Windows and case-sensitive elsewhere,
// and target == base is reported as NOT under base.
func IsUnderDir(base, target string) (bool, error) {
	cb, err := canonicalizePath(base)
	if err != nil {
		return false, fmt.Errorf("resolve base %q: %w", base, err)
	}
	ct, err := canonicalizePath(target)
	if err != nil {
		return false, fmt.Errorf("resolve target %q: %w", target, err)
	}
	return containsCanonical(cb, ct), nil
}

// AssertUnderWorktreeDir returns nil iff targetPath is strictly contained under
// managedDir. The caller MUST NOT perform a destructive operation (os.RemoveAll,
// git worktree remove) on targetPath if this returns a non-nil error (F-09).
func AssertUnderWorktreeDir(managedDir, targetPath string) error {
	under, err := IsUnderDir(managedDir, targetPath)
	if err != nil {
		return fmt.Errorf("%w: cannot verify %q is under %q: %v", ErrUnsafeWorktreePath, targetPath, managedDir, err)
	}
	if !under {
		return fmt.Errorf("%w: %q is not under %q", ErrUnsafeWorktreePath, targetPath, managedDir)
	}
	return nil
}

// WorktreesDir exposes the managed worktrees directory to callers in other
// packages (instance.go, winhost) so they can assert containment before a
// destructive operation.
func WorktreesDir() (string, error) {
	return getWorktreeDirectory()
}

// AssertWorktreePathContained resolves the current managed worktrees directory
// and asserts that targetPath is strictly under it. It is the single entry point
// every os.RemoveAll / worktree-remove callsite should use to guard against a
// tampered state.json / workspaces.json pointing the worktree path at an
// arbitrary directory (F-09).
func AssertWorktreePathContained(targetPath string) error {
	dir, err := getWorktreeDirectory()
	if err != nil {
		return fmt.Errorf("%w: cannot determine worktrees dir: %v", ErrUnsafeWorktreePath, err)
	}
	return AssertUnderWorktreeDir(dir, targetPath)
}

// IsLocalGitRepo reports whether p is a non-empty absolute path that exists on
// disk and is the root (or inside) a local git repository. It is used to reject
// a poisoned OriginRoot/repoPath before `git worktree add` can run a
// post-checkout hook from an attacker-controlled repo (F-03).
func IsLocalGitRepo(p string) bool {
	if strings.TrimSpace(p) == "" || !filepath.IsAbs(p) {
		return false
	}
	if info, err := os.Stat(p); err != nil || !info.IsDir() {
		return false
	}
	return IsGitRepo(p)
}
