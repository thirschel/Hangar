//go:build windows

package winhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	cslog "claude-squad/log"
	"claude-squad/session/agentcmd"
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

// TestAgentCommandsForWorkspaceIntent covers the command builders used by
// workspace create (seed a new session id) and revive (resume an existing id).
func TestAgentCommandsForWorkspaceIntent(t *testing.T) {
	seedCases := []struct {
		program, id, want string
	}{
		{"copilot", "abc-123", "copilot --session-id=abc-123"},
		{"copilot", "", "copilot"},      // no id -> unchanged
		{"bash", "abc-123", "bash"},     // unknown agent -> unchanged
		{"claude", "abc-123", "claude"}, // not yet verified -> unchanged
	}
	for _, c := range seedCases {
		if got := agentcmd.SeedNewCommand(c.program, c.id); got != c.want {
			t.Fatalf("SeedNewCommand(%q, %q) = %q, want %q", c.program, c.id, got, c.want)
		}
	}

	resumeCases := []struct {
		program, id, want string
	}{
		{"copilot", "abc-123", "copilot --resume=abc-123"},
		{"copilot", "", "copilot"},      // no id -> unchanged
		{"bash", "abc-123", "bash"},     // unknown agent -> unchanged
		{"claude", "abc-123", "claude"}, // not yet verified -> unchanged
	}
	for _, c := range resumeCases {
		if got := agentcmd.ResumeCommand(c.program, c.id); got != c.want {
			t.Fatalf("ResumeCommand(%q, %q) = %q, want %q", c.program, c.id, got, c.want)
		}
	}

	if !agentcmd.SupportsResume("copilot") ||
		!agentcmd.SupportsResume(`C:\Tools\copilot.exe`) ||
		!agentcmd.SupportsResume("copilot.cmd --verbose") {
		t.Fatal("expected copilot executable names to support resume")
	}
	if agentcmd.SupportsResume("cmd.exe /c copilot") ||
		agentcmd.SupportsResume("claude") ||
		agentcmd.SupportsResume("cmd") {
		t.Fatal("expected non-copilot agents to not (yet) support resume")
	}
}

func TestCopilotWorkspaceLaunchCommands(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	bin := t.TempDir()
	copilotCmd := filepath.Join(bin, "copilot.cmd")
	if err := os.WriteFile(copilotCmd, []byte("@echo off\r\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	repo := initTempRepo(t)

	pipe, cleanup := startTestHost(t)
	defer cleanup()
	c, err := dialClient(pipe, 3*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer c.Close()

	ws, err := c.CreateWorkspace(repo, "Copilot Launch", "copilot.cmd", "")
	if err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}

	sessionProgram := func() string {
		t.Helper()
		sessions, err := c.ListSessions()
		if err != nil {
			t.Fatalf("ListSessions: %v", err)
		}
		for _, s := range sessions {
			if s.Name == ws.SessionName {
				return s.Program
			}
		}
		t.Fatalf("session %s not found in %+v", ws.SessionName, sessions)
		return ""
	}

	seedProgram := sessionProgram()
	const seedPrefix = "copilot.cmd --session-id="
	if !strings.HasPrefix(seedProgram, seedPrefix) || strings.Contains(seedProgram, "--resume=") {
		t.Fatalf("create command = %q, want seed command with --session-id", seedProgram)
	}
	agentID := strings.TrimPrefix(seedProgram, seedPrefix)
	if agentID == "" {
		t.Fatalf("create command had empty agent session id: %q", seedProgram)
	}

	if err := c.Kill(ws.SessionName); err != nil {
		t.Fatalf("Kill: %v", err)
	}
	if _, _, err := c.Attach(ws.SessionName, 120, 30); err != nil {
		t.Fatalf("Attach should revive workspace: %v", err)
	}
	if got, want := sessionProgram(), "copilot.cmd --resume="+agentID; got != want {
		t.Fatalf("revive command = %q, want %q", got, want)
	}
}

// TestNewUUID checks the session id is a well-formed v4 UUID and unique.
func TestNewUUID(t *testing.T) {
	re := regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	a, b := newUUID(), newUUID()
	if !re.MatchString(a) {
		t.Fatalf("newUUID() = %q, not a v4 UUID", a)
	}
	if a == b {
		t.Fatalf("newUUID() returned duplicates: %q", a)
	}
}

// agent session is gone (as it is after a daemon restart, when only metadata is
// reloaded), attaching to that session must transparently revive it from the
// persisted program/worktree instead of failing with "no such session".
func TestReviveSessionOnAttach(t *testing.T) {
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

	ws, err := c.CreateWorkspace(repo, "Revive Me", "cmd", "")
	if err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}

	// Simulate the post-restart state: the agent session is gone, the workspace
	// metadata remains.
	if err := c.Kill(ws.SessionName); err != nil {
		t.Fatalf("Kill: %v", err)
	}
	if exists, _, _ := c.HasSession(ws.SessionName); exists {
		t.Fatalf("expected session %s to be gone after Kill", ws.SessionName)
	}

	// Attaching must revive the session rather than error.
	p, tok, err := c.Attach(ws.SessionName, 120, 30)
	if err != nil {
		t.Fatalf("Attach should revive the workspace session, got: %v", err)
	}
	if p == "" || tok == "" {
		t.Fatalf("revive attach returned empty pipe/token: pipe=%q token=%q", p, tok)
	}

	// The workspace should report alive again.
	list, err := c.ListWorkspaces()
	if err != nil {
		t.Fatalf("ListWorkspaces: %v", err)
	}
	found := false
	for _, w := range list {
		if w.ID == ws.ID {
			found = true
			if !w.Alive {
				t.Fatalf("expected revived workspace to be alive")
			}
		}
	}
	if !found {
		t.Fatalf("workspace %s missing from list after revive", ws.ID)
	}
}

func TestSanitizeTitle(t *testing.T) {
	cases := map[string]string{
		"Add login flow":                 "Add login flow",
		`  "Refactor auth module"  `:     "Refactor auth module",
		"\x1b[32mFix flaky tests\x1b[0m": "Fix flaky tests",
		"Title one\nTitle two":           "Title one",
		"\n\n   Trimmed title  \n":       "Trimmed title",
		"### Heading title":              "Heading title",
		"":                               "",
	}
	for in, want := range cases {
		if got := sanitizeTitle(in); got != want {
			t.Errorf("sanitizeTitle(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestTruncateTitle(t *testing.T) {
	if got := truncateTitle("one two three four five six seven eight nine ten"); len(strings.Fields(got)) > 8 {
		t.Errorf("truncateTitle kept too many words: %q", got)
	}
	if got := truncateTitle(strings.Repeat("x", 200)); len(got) > 60 {
		t.Errorf("truncateTitle did not cap length: len=%d", len(got))
	}
}

func TestDeriveTitle(t *testing.T) {
	if got := deriveTitle("\n  Implement the parser  \nmore detail"); got != "Implement the parser" {
		t.Errorf("deriveTitle first line = %q", got)
	}
	if got := deriveTitle("   \n  "); got != "workspace" {
		t.Errorf("deriveTitle empty fallback = %q", got)
	}
}

func TestDefaultTitle(t *testing.T) {
	cases := map[string]string{
		`C:\dev\claude-squad`:  "claude-squad",
		`C:\dev\claude-squad\`: "claude-squad",
		`D:\repos\my-app\`:     "my-app",
		"":                     "workspace",
	}
	for in, want := range cases {
		if got := defaultTitle(in); got != want {
			t.Errorf("defaultTitle(%q) = %q, want %q", in, got, want)
		}
	}
}
