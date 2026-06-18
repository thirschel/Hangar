package git

import (
	"context"
	"fmt"
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

// Diff returns the git diff between the worktree and the base branch along with statistics
func (g *GitWorktree) Diff() *DiffStats {
	stats := &DiffStats{}

	sha := g.GetBaseCommitSHA()
	if err := ValidateSHA(sha); err != nil {
		stats.Error = fmt.Errorf("invalid base commit SHA: %w", err)
		return stats
	}

	// -N stages untracked files (intent to add), including them in the diff
	_, err := g.runGitCommand(g.worktreePath, "add", "-N", ".")
	if err != nil {
		stats.Error = err
		return stats
	}

	content, err := g.runGitCommand(g.worktreePath, "--no-pager", "diff", sha)
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

	// -N stages untracked files (intent to add), including them in the diff
	_, err := g.runGitCommand(g.worktreePath, "add", "-N", ".")
	if err != nil {
		stats.Error = err
		return stats
	}

	out, err := g.runGitCommand(g.worktreePath, "--no-pager", "diff", "--numstat", sha)
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

	// -N stages untracked files (intent to add), including them in the diff.
	if _, err := g.runGitCommandContext(ctx, g.worktreePath, "add", "-N", "."); err != nil {
		stats.Error = err
		return stats
	}

	out, err := g.runGitCommandContext(ctx, g.worktreePath, "--no-pager", "diff", "--numstat", sha)
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
	// -N stages untracked files (intent to add) so they show up in the diff.
	if _, err := g.runGitCommand(g.worktreePath, "add", "-N", "."); err != nil {
		return nil, err
	}
	out, err := g.runGitCommand(g.worktreePath, "--no-pager", "diff", "--numstat", sha)
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
	if _, err := g.runGitCommand(g.worktreePath, "add", "-N", "."); err != nil {
		return "", err
	}
	return g.runGitCommand(g.worktreePath, "--no-pager", "diff", sha, "--", path)
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
