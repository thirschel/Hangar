//go:build windows

package winhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	cslog "claude-squad/log"
)

// TestMain initializes the global logger so tests that drive config/git (e.g.
// workspace creation) don't nil-panic on log.ErrorLog.
func TestMain(m *testing.M) {
	cslog.Initialize(false)
	code := m.Run()
	cslog.Close()
	os.Exit(code)
}

// initTempRepo creates a temp git repo with one commit and returns its path.
func initTempRepo(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init")
	run("checkout", "-b", "main")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("# repo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	run("commit", "-m", "init")
	return repo
}

// TestWorkspaceLifecycle covers the core-daemon workspace RPC (E1): create a
// workspace (worktree+branch+agent session), see it listed with diff stats,
// fetch the changed-file diff, and archive it (cleaning up worktree + session).
// Runs against an isolated config dir so it never touches real ~/.claude-squad.
func TestWorkspaceLifecycle(t *testing.T) {
	// Isolate config/worktrees/workspaces.json (Windows uses USERPROFILE).
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	repo := initTempRepo(t)

	pipe, cleanup := startRealHost(t)
	defer cleanup()
	c, err := dialClient(pipe, 3*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer c.Close()

	// Create a workspace (use a long-lived program on PATH so the session stays alive).
	ws, err := c.CreateWorkspace(repo, "My Feature", "cmd", "")
	if err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}
	if ws == nil || ws.Branch == "" || ws.WorktreePath == "" || ws.SessionName == "" {
		t.Fatalf("incomplete workspace info: %+v", ws)
	}
	if _, err := os.Stat(ws.WorktreePath); err != nil {
		t.Fatalf("worktree path not created: %v", err)
	}
	if !ws.Alive {
		t.Fatalf("expected workspace agent session to be alive")
	}

	// Simulate an agent edit, then verify the diff surfaces it.
	if err := os.WriteFile(filepath.Join(ws.WorktreePath, "NEW.txt"), []byte("hello\nworld\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	files, err := c.WorkspaceFiles(ws.ID)
	if err != nil {
		t.Fatalf("WorkspaceFiles: %v", err)
	}
	found := false
	for _, f := range files {
		if strings.Contains(f.Path, "NEW.txt") && f.Added >= 2 {
			found = true
		}
	}

	if !found {
		t.Fatalf("expected NEW.txt with +2 in changed files, got %+v", files)
	}
	fd, err := c.WorkspaceFileDiff(ws.ID, "NEW.txt")
	if err != nil || !strings.Contains(fd, "NEW.txt") {
		t.Fatalf("WorkspaceFileDiff: err=%v diff=%q", err, fd)
	}

	// It should appear in the list.
	list, err := c.ListWorkspaces()
	if err != nil || len(list) != 1 || list[0].ID != ws.ID {
		t.Fatalf("ListWorkspaces: err=%v list=%+v", err, list)
	}

	// Archive removes the worktree and the agent session.
	if err := c.ArchiveWorkspace(ws.ID); err != nil {
		t.Fatalf("ArchiveWorkspace: %v", err)
	}
	if _, err := os.Stat(ws.WorktreePath); !os.IsNotExist(err) {
		t.Fatalf("expected worktree removed after archive, stat err=%v", err)
	}
	list, err = c.ListWorkspaces()
	if err != nil || len(list) != 0 {
		t.Fatalf("expected empty list after archive, got err=%v list=%+v", err, list)
	}
}

func TestWorkspaceCommit(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("GIT_AUTHOR_NAME", "t")
	t.Setenv("GIT_AUTHOR_EMAIL", "t@t")
	t.Setenv("GIT_COMMITTER_NAME", "t")
	t.Setenv("GIT_COMMITTER_EMAIL", "t@t")

	repo := initTempRepo(t)

	pipe, cleanup := startTestHost(t)
	defer cleanup()
	c, err := dialClient(pipe, 3*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer c.Close()

	ws, err := c.CreateWorkspace(repo, "Commit Test", "cmd", "")
	if err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}
	defer func() { _ = c.ArchiveWorkspace(ws.ID) }()

	if err := os.WriteFile(filepath.Join(ws.WorktreePath, "COMMIT.txt"), []byte("committed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	const message = "workspace commit test"
	if err := c.CommitWorkspace(ws.ID, message); err != nil {
		t.Fatalf("CommitWorkspace: %v", err)
	}

	cmd := exec.Command("git", "--no-pager", "log", "--oneline", "-1")
	cmd.Dir = ws.WorktreePath
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git log: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), message) {
		t.Fatalf("latest commit %q does not contain %q", out, message)
	}
}

// TestCreateWorkspaceRejectsUnknownProgram verifies the daemon validates the
// agent program *before* creating any worktree or session: a bogus program must
// fail fast with a clear "not found on PATH" error and leave nothing behind (no
// workspace entry, no orphan worktree). This guards the MVP regression where a
// stale "test-program" default produced a half-created workspace + dead session.
func TestCreateWorkspaceRejectsUnknownProgram(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	repo := initTempRepo(t)

	pipe, cleanup := startRealHost(t)
	defer cleanup()
	c, err := dialClient(pipe, 3*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer c.Close()

	_, err = c.CreateWorkspace(repo, "Bad Agent", "definitely-not-a-real-program-xyz", "")
	if err == nil {
		t.Fatalf("expected CreateWorkspace to fail for an unknown program")
	}
	if !strings.Contains(err.Error(), "not found on PATH") {
		t.Fatalf("expected a 'not found on PATH' error, got: %v", err)
	}

	// No workspace should have been recorded.
	list, err := c.ListWorkspaces()
	if err != nil {
		t.Fatalf("ListWorkspaces: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("expected no workspaces after a failed create, got %d: %+v", len(list), list)
	}

	// And no orphan worktree should have been left under ~/.claude-squad/worktrees.
	wtRoot := filepath.Join(home, ".claude-squad", "worktrees")
	if entries, err := os.ReadDir(wtRoot); err == nil {
		if len(entries) != 0 {
			t.Fatalf("expected no orphan worktrees, found %d under %s", len(entries), wtRoot)
		}
	}
}
