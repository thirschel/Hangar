package git

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSetupFromExistingBranch_RemovesOrphanedDirectory(t *testing.T) {
	tempHome := t.TempDir()
	originalHome := os.Getenv("HOME")
	if err := os.Setenv("HOME", tempHome); err != nil {
		t.Fatalf("set HOME: %v", err)
	}
	defer func() {
		_ = os.Setenv("HOME", originalHome)
	}()
	// On Windows the config (and worktrees) dir is derived from USERPROFILE, so
	// point it at the same temp home; otherwise the worktree-containment guard
	// resolves the managed dir to the real profile and rejects the temp path.
	t.Setenv("USERPROFILE", tempHome)

	repoPath := filepath.Join(t.TempDir(), "repo")
	mustRunGit(t, "", "init", repoPath)
	mustRunGit(t, repoPath, "config", "user.name", "Test User")
	mustRunGit(t, repoPath, "config", "user.email", "test@example.com")

	readmePath := filepath.Join(repoPath, "README.md")
	if err := os.WriteFile(readmePath, []byte("hello\n"), 0644); err != nil {
		t.Fatalf("write README: %v", err)
	}

	mustRunGit(t, repoPath, "add", "README.md")
	mustRunGit(t, repoPath, "commit", "-m", "initial")
	mustRunGit(t, repoPath, "branch", "feature/test")

	worktreePath := filepath.Join(tempHome, ".claude-squad", "worktrees", "feature-test")
	if err := os.MkdirAll(worktreePath, 0755); err != nil {
		t.Fatalf("mkdir orphaned worktree: %v", err)
	}

	junkPath := filepath.Join(worktreePath, "orphan.txt")
	if err := os.WriteFile(junkPath, []byte("orphaned\n"), 0644); err != nil {
		t.Fatalf("write orphan marker: %v", err)
	}

	g := &GitWorktree{
		repoPath:         repoPath,
		worktreePath:     worktreePath,
		branchName:       "feature/test",
		isExistingBranch: true,
	}

	if err := g.Setup(); err != nil {
		t.Fatalf("Setup() error = %v", err)
	}

	if _, err := os.Stat(junkPath); !os.IsNotExist(err) {
		t.Fatalf("orphan marker still exists after Setup, err = %v", err)
	}

	if valid, err := g.IsValidWorktree(); err != nil {
		t.Fatalf("IsValidWorktree() error = %v", err)
	} else if !valid {
		t.Fatal("expected Setup() to recreate a valid worktree")
	}

	currentBranch := mustRunGit(t, worktreePath, "branch", "--show-current")
	if currentBranch != "feature/test\n" {
		t.Fatalf("current branch = %q, want %q", currentBranch, "feature/test\n")
	}
}

func mustRunGit(t *testing.T, dir string, args ...string) string {
	t.Helper()

	cmdArgs := args
	if dir != "" {
		cmdArgs = append([]string{"-C", dir}, args...)
	}

	cmd := exec.Command("git", cmdArgs...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, output)
	}
	return string(output)
}

func TestSetupNewWorktreeWithBaseCommitOverride(t *testing.T) {
	tempHome := t.TempDir()
	t.Setenv("HOME", tempHome)
	t.Setenv("USERPROFILE", tempHome)

	repoPath, originalBranch, firstSHA, secondSHA := setupTwoCommitRepo(t)

	worktree, _, err := NewGitWorktree(repoPath, "basetest")
	require.NoError(t, err)
	worktree.SetBaseCommitOverride(firstSHA)
	require.NoError(t, worktree.Setup())
	defer func() {
		require.NoError(t, worktree.Cleanup())
	}()

	worktreeHEAD := strings.TrimSpace(mustRunGit(t, worktree.GetWorktreePath(), "rev-parse", "HEAD"))
	require.Equal(t, firstSHA, worktreeHEAD)
	require.Equal(t, firstSHA, worktree.GetBaseCommitSHA())

	newBranchSHA := strings.TrimSpace(mustRunGit(t, repoPath, "rev-parse", worktree.GetBranchName()))
	require.Equal(t, firstSHA, newBranchSHA)
	originalBranchSHA := strings.TrimSpace(mustRunGit(t, repoPath, "rev-parse", originalBranch))
	require.Equal(t, secondSHA, originalBranchSHA)
}

func TestSetupNewWorktreeWithUnavailableBaseCommitOverrideFallsBackToHEAD(t *testing.T) {
	tempHome := t.TempDir()
	t.Setenv("HOME", tempHome)
	t.Setenv("USERPROFILE", tempHome)

	repoPath, _, _, secondSHA := setupTwoCommitRepo(t)

	worktree, _, err := NewGitWorktree(repoPath, "fallbacktest")
	require.NoError(t, err)
	worktree.SetBaseCommitOverride("deadbeefdeadbeefdeadbeefdeadbeefdeadbeef")
	require.NoError(t, worktree.Setup())
	defer func() {
		require.NoError(t, worktree.Cleanup())
	}()

	worktreeHEAD := strings.TrimSpace(mustRunGit(t, worktree.GetWorktreePath(), "rev-parse", "HEAD"))
	require.Equal(t, secondSHA, worktreeHEAD)
	require.Equal(t, secondSHA, worktree.GetBaseCommitSHA())
}

func setupTwoCommitRepo(t *testing.T) (repoPath, originalBranch, firstSHA, secondSHA string) {
	t.Helper()

	repoPath = filepath.Join(t.TempDir(), "repo")
	mustRunGit(t, "", "init", repoPath)
	mustRunGit(t, repoPath, "config", "user.name", "Test User")
	mustRunGit(t, repoPath, "config", "user.email", "test@example.com")

	firstPath := filepath.Join(repoPath, "first.txt")
	require.NoError(t, os.WriteFile(firstPath, []byte("first\n"), 0644))
	mustRunGit(t, repoPath, "add", "first.txt")
	mustRunGit(t, repoPath, "commit", "-m", "first")
	firstSHA = strings.TrimSpace(mustRunGit(t, repoPath, "rev-parse", "HEAD"))
	originalBranch = strings.TrimSpace(mustRunGit(t, repoPath, "branch", "--show-current"))

	secondPath := filepath.Join(repoPath, "second.txt")
	require.NoError(t, os.WriteFile(secondPath, []byte("second\n"), 0644))
	mustRunGit(t, repoPath, "add", "second.txt")
	mustRunGit(t, repoPath, "commit", "-m", "second")
	secondSHA = strings.TrimSpace(mustRunGit(t, repoPath, "rev-parse", "HEAD"))

	return repoPath, originalBranch, firstSHA, secondSHA
}
