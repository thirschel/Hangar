package git

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPushChangesPushesBranchToOrigin(t *testing.T) {
	repoPath := filepath.Join(t.TempDir(), "repo")
	remotePath := filepath.Join(t.TempDir(), "remote.git")

	mustRunGit(t, "", "init", "--bare", remotePath)
	mustRunGit(t, "", "init", repoPath)
	mustRunGit(t, repoPath, "config", "user.name", "Test User")
	mustRunGit(t, repoPath, "config", "user.email", "test@example.com")
	mustRunGit(t, repoPath, "remote", "add", "origin", remotePath)

	readmePath := filepath.Join(repoPath, "README.md")
	require.NoError(t, os.WriteFile(readmePath, []byte("hello\n"), 0o644))
	mustRunGit(t, repoPath, "add", "README.md")
	mustRunGit(t, repoPath, "commit", "-m", "initial")
	mustRunGit(t, repoPath, "push", "-u", "origin", "HEAD")
	mustRunGit(t, repoPath, "checkout", "-b", "feature/push")

	require.NoError(t, os.WriteFile(readmePath, []byte("hello\nworld\n"), 0o644))

	g := &GitWorktree{
		repoPath:     repoPath,
		worktreePath: repoPath,
		branchName:   "feature/push",
	}

	require.NoError(t, g.PushChanges("push changes", false))

	localHead := strings.TrimSpace(mustRunGit(t, repoPath, "rev-parse", "HEAD"))
	remoteHead := strings.TrimSpace(mustRunGit(t, "", "--git-dir", remotePath, "rev-parse", "feature/push"))
	require.Equal(t, localHead, remoteHead)
	require.Equal(t, "origin/feature/push", strings.TrimSpace(mustRunGit(t, repoPath, "rev-parse", "--abbrev-ref", "feature/push@{upstream}")))
}
