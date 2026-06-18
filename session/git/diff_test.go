package git

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestParseNumstat(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		wantAdded   int
		wantRemoved int
	}{
		{
			name:        "empty output",
			input:       "",
			wantAdded:   0,
			wantRemoved: 0,
		},
		{
			name:        "single file",
			input:       "3\t1\tfoo.go\n",
			wantAdded:   3,
			wantRemoved: 1,
		},
		{
			name:        "multiple files sum correctly",
			input:       "3\t1\tfoo.go\n10\t2\tbar/baz.go\n",
			wantAdded:   13,
			wantRemoved: 3,
		},
		{
			name:        "binary files are skipped",
			input:       "5\t0\tfoo.go\n-\t-\timage.png\n2\t2\tbar.go\n",
			wantAdded:   7,
			wantRemoved: 2,
		},
		{
			name:        "path with tabs is preserved via SplitN",
			input:       "4\t4\tpath\twith\ttabs.go\n",
			wantAdded:   4,
			wantRemoved: 4,
		},
		{
			name:        "trailing newlines do not add garbage",
			input:       "1\t0\ta.go\n\n\n",
			wantAdded:   1,
			wantRemoved: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotAdded, gotRemoved := parseNumstat(tt.input)
			if gotAdded != tt.wantAdded || gotRemoved != tt.wantRemoved {
				t.Errorf("parseNumstat(%q) = (%d, %d), want (%d, %d)",
					tt.input, gotAdded, gotRemoved, tt.wantAdded, tt.wantRemoved)
			}
		})
	}
}

func TestDiffNumstatTimeout(t *testing.T) {
	tempHome := t.TempDir()
	t.Setenv("HOME", tempHome)
	t.Setenv("USERPROFILE", tempHome)

	repoPath, _, firstSHA, _ := setupTwoCommitRepo(t)

	worktree, _, err := NewGitWorktree(repoPath, "difftimeouttest")
	require.NoError(t, err)
	worktree.SetBaseCommitOverride(firstSHA)
	require.NoError(t, worktree.Setup())
	defer func() { require.NoError(t, worktree.Cleanup()) }()

	// Add an untracked two-line file; `git add -N .` should fold it into numstat.
	added := filepath.Join(worktree.GetWorktreePath(), "added.txt")
	require.NoError(t, os.WriteFile(added, []byte("line1\nline2\n"), 0644))

	// Happy path: a generous timeout returns the real counts with no error.
	stats := worktree.DiffNumstatTimeout(15 * time.Second)
	require.NoError(t, stats.Error)
	require.GreaterOrEqual(t, stats.Added, 2)

	// An already-expired deadline must surface an error rather than blocking.
	expired := worktree.DiffNumstatTimeout(time.Nanosecond)
	require.Error(t, expired.Error)
}
