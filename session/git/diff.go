package git

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// DiffStats holds statistics about the changes in a diff
type DiffStats struct {
	// Content is the full diff content
	Content string
	// Added is the number of added lines
	Added int
	// Removed is the number of removed lines
	Removed int
	// Error holds any error that occurred during diff computation
	// This allows propagating setup errors (like missing base commit) without breaking the flow
	Error error
}

func (d *DiffStats) IsEmpty() bool {
	return d.Added == 0 && d.Removed == 0 && d.Content == ""
}

// stageUntrackedForDiff makes untracked files visible to a subsequent
// `git diff <base>` (which otherwise omits them). For a managed worktree it runs
// the cheap `git add -N .` against the worktree's own disposable index. For an
// in-place session (noStage) it instead seeds a throwaway index from HEAD and
// marks intent-to-add there, returning the GIT_INDEX_FILE env to use for the
// diff command — so the user's real repo index is never modified or locked by
// the background diff refresh. The returned cleanup must always be called.
func (g *GitWorktree) stageUntrackedForDiff(ctx context.Context) (env []string, cleanup func(), err error) {
	cleanup = func() {}
	if !g.noStage {
		if _, err := g.runGitCommandContext(ctx, g.worktreePath, "add", "-N", "."); err != nil {
			return nil, cleanup, err
		}
		return nil, cleanup, nil
	}

	tmp, terr := os.CreateTemp("", "hangar-diff-index-*")
	if terr != nil {
		return nil, cleanup, terr
	}
	idx := tmp.Name()
	_ = tmp.Close()
	cleanup = func() { _ = os.Remove(idx) }
	env = []string{"GIT_INDEX_FILE=" + idx}

	// Seed the throwaway index with HEAD so tracked files are recognized, then
	// mark untracked files intent-to-add in that index only.
	if _, err := g.runGitCommandEnvContext(ctx, env, g.worktreePath, "read-tree", "HEAD"); err != nil {
		cleanup()
		return nil, func() {}, err
	}
	if _, err := g.runGitCommandEnvContext(ctx, env, g.worktreePath, "add", "-N", "."); err != nil {
		cleanup()
		return nil, func() {}, err
	}
	return env, cleanup, nil
}

// Diff returns the git diff between the worktree and the base branch along with statistics
func (g *GitWorktree) Diff() *DiffStats {
	stats := &DiffStats{}

	sha := g.GetBaseCommitSHA()
	if err := ValidateSHA(sha); err != nil {
		stats.Error = fmt.Errorf("invalid base commit SHA: %w", err)
		return stats
	}

	ctx := context.Background()
	env, cleanup, err := g.stageUntrackedForDiff(ctx)
	if err != nil {
		stats.Error = err
		return stats
	}
	defer cleanup()

	content, err := g.runGitCommandEnvContext(ctx, env, g.worktreePath, "--no-pager", "diff", sha)
	if err != nil {
		stats.Error = err
		return stats
	}
	lines := strings.Split(content, "\n")
	for _, line := range lines {
		if strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++") {
			stats.Added++
		} else if strings.HasPrefix(line, "-") && !strings.HasPrefix(line, "---") {
			stats.Removed++
		}
	}
	stats.Content = content

	return stats
}

// DiffNumstat returns the added/removed line counts between the worktree and the
// base branch without loading the full diff content into memory. Use this when
// only the summary counts are needed (e.g. for unselected instances in the list).
func (g *GitWorktree) DiffNumstat() *DiffStats {
	stats := &DiffStats{}

	sha := g.GetBaseCommitSHA()
	if err := ValidateSHA(sha); err != nil {
		stats.Error = fmt.Errorf("invalid base commit SHA: %w", err)
		return stats
	}

	ctx := context.Background()
	env, cleanup, err := g.stageUntrackedForDiff(ctx)
	if err != nil {
		stats.Error = err
		return stats
	}
	defer cleanup()

	out, err := g.runGitCommandEnvContext(ctx, env, g.worktreePath, "--no-pager", "diff", "--numstat", sha)
	if err != nil {
		stats.Error = err
		return stats
	}

	stats.Added, stats.Removed = parseNumstat(out)
	return stats
}

// DiffNumstatTimeout is like DiffNumstat but bounds the underlying git commands
// with a timeout. `git add -N .` walks the entire worktree, so a worktree that
// contains a pathological tree (e.g. a symlink into the Windows assembly cache)
// can otherwise make this take minutes. On timeout the git process is killed and
// the returned DiffStats carries the timeout error, so callers can fall back to
// the last known counts instead of blocking.
func (g *GitWorktree) DiffNumstatTimeout(timeout time.Duration) *DiffStats {
	stats := &DiffStats{}

	sha := g.GetBaseCommitSHA()
	if err := ValidateSHA(sha); err != nil {
		stats.Error = fmt.Errorf("invalid base commit SHA: %w", err)
		return stats
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	env, cleanup, err := g.stageUntrackedForDiff(ctx)
	if err != nil {
		stats.Error = err
		return stats
	}
	defer cleanup()

	out, err := g.runGitCommandEnvContext(ctx, env, g.worktreePath, "--no-pager", "diff", "--numstat", sha)
	if err != nil {
		stats.Error = err
		return stats
	}

	stats.Added, stats.Removed = parseNumstat(out)
	return stats
}

type FileDiffStat struct {
	Path    string
	Added   int
	Removed int
}

// ChangedFiles returns the files changed between the worktree and the base
// branch, each with added/removed line counts (via `git diff --numstat`).
func (g *GitWorktree) ChangedFiles() ([]FileDiffStat, error) {
	sha := g.GetBaseCommitSHA()
	if err := ValidateSHA(sha); err != nil {
		return nil, fmt.Errorf("invalid base commit SHA: %w", err)
	}
	ctx := context.Background()
	env, cleanup, err := g.stageUntrackedForDiff(ctx)
	if err != nil {
		return nil, err
	}
	defer cleanup()
	out, err := g.runGitCommandEnvContext(ctx, env, g.worktreePath, "--no-pager", "diff", "--numstat", sha)
	if err != nil {
		return nil, err
	}
	var files []FileDiffStat
	for _, line := range strings.Split(out, "\n") {
		if line == "" {
			continue
		}
		fields := strings.SplitN(line, "\t", 3)
		if len(fields) < 3 {
			continue
		}
		a, _ := strconv.Atoi(fields[0]) // "-" for binary -> 0
		r, _ := strconv.Atoi(fields[1])
		files = append(files, FileDiffStat{Path: fields[2], Added: a, Removed: r})
	}
	return files, nil
}

// FileDiff returns the unified diff for a single file vs the base branch.
func (g *GitWorktree) FileDiff(path string) (string, error) {
	sha := g.GetBaseCommitSHA()
	if err := ValidateSHA(sha); err != nil {
		return "", fmt.Errorf("invalid base commit SHA: %w", err)
	}
	ctx := context.Background()
	env, cleanup, err := g.stageUntrackedForDiff(ctx)
	if err != nil {
		return "", err
	}
	defer cleanup()
	return g.runGitCommandEnvContext(ctx, env, g.worktreePath, "--no-pager", "diff", sha, "--", path)
}

// Each line is formatted as <added>\t<removed>\t<path>. Binary files report
// "-\t-\t<path>" and are ignored for line totals.
func parseNumstat(out string) (added int, removed int) {
	for _, line := range strings.Split(out, "\n") {
		if line == "" {
			continue
		}
		fields := strings.SplitN(line, "\t", 3)
		if len(fields) < 2 {
			continue
		}
		a, aerr := strconv.Atoi(fields[0])
		r, rerr := strconv.Atoi(fields[1])
		if aerr != nil || rerr != nil {
			continue
		}
		added += a
		removed += r
	}
	return added, removed
}
