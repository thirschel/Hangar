//go:build windows

package winhost

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	cslog "hangar/log"
	"hangar/session/agentcmd"
	"hangar/session/winhost/proto"
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
// Runs against an isolated config dir so it never touches real ~/.hangar.
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
	authClient(t, c)

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

	// Archive removes the worktree and the agent session. The teardown (kill +
	// RemoveAll + git) now runs in the background so the control pipe isn't blocked,
	// so poll for the worktree to disappear instead of expecting it synchronously.
	if err := c.ArchiveWorkspace(ws.ID, true); err != nil {
		t.Fatalf("ArchiveWorkspace: %v", err)
	}
	removed := false
	for i := 0; i < 100; i++ {
		if _, err := os.Stat(ws.WorktreePath); os.IsNotExist(err) {
			removed = true
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !removed {
		t.Fatalf("expected worktree removed after archive (waited 5s), path still present: %s", ws.WorktreePath)
	}
	// The registry removal is synchronous, so the list is empty immediately.
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
	authClient(t, c)

	ws, err := c.CreateWorkspace(repo, "Commit Test", "cmd", "")
	if err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}
	defer func() { _ = c.ArchiveWorkspace(ws.ID, true) }()

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
	authClient(t, c)

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

	// And no orphan worktree should have been left under ~/.hangar/worktrees.
	wtRoot := filepath.Join(home, ".hangar", "worktrees")
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
	authClient(t, c)

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

func TestRevivePlanRoutesRichAndTerminal(t *testing.T) {
	const validID = "123e4567-e89b-12d3-a456-426614174000"

	cases := []struct {
		name          string
		w             *workspace
		wantRich      bool
		wantResume    bool
		wantProgram   string
		wantAgentID   string
		wantInvalidID bool
		wantMissingID bool
	}{
		{
			name:        "rich valid resumes via sdk",
			w:           &workspace{Kind: proto.WorkspaceKindRich, Program: "copilot", AgentSessionID: validID},
			wantRich:    true,
			wantResume:  true,
			wantProgram: "copilot",
			wantAgentID: validID,
		},
		{
			name:          "rich invalid launches fresh",
			w:             &workspace{Kind: proto.WorkspaceKindRich, Program: "copilot", AgentSessionID: "bad;id"},
			wantRich:      true,
			wantProgram:   "copilot",
			wantInvalidID: true,
		},
		{
			name:          "rich missing launches fresh",
			w:             &workspace{Kind: proto.WorkspaceKindRich, Program: "copilot"},
			wantRich:      true,
			wantProgram:   "copilot",
			wantMissingID: true,
		},
		{
			name:        "terminal copilot resumes via command flag",
			w:           &workspace{Program: "cpa", CopilotResume: true, AgentSessionID: validID},
			wantProgram: "cpa --resume=" + validID,
		},
		{
			name:          "terminal invalid launches fresh",
			w:             &workspace{Program: "copilot", AgentSessionID: "bad;id"},
			wantProgram:   "copilot",
			wantInvalidID: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := revivePlanForWorkspace(tc.w)
			if got.rich != tc.wantRich || got.resume != tc.wantResume ||
				got.program != tc.wantProgram || got.agentSessionID != tc.wantAgentID ||
				got.invalidSessionID != tc.wantInvalidID || got.missingSessionID != tc.wantMissingID {
				t.Fatalf("revivePlanForWorkspace() = %+v", got)
			}
		})
	}
}

// TestResumeCopilotSessionReportsRichKind guards the session-browser "open as
// rich" resume path: a browser-resumed Copilot session must (a) start through the
// SDK ("rich") backend so the live session is a *sdkSession the desktop can attach
// to via OpenRichStream, and (b) report Kind == rich through toInfo so AgentMode
// keeps it. Previously this path started a TERMINAL ConPTY session yet persisted
// Kind=rich, so OpenRichStream's *sdkSession assertion failed and AgentMode still
// worked only by luck of the Kind tag. The SDK hook keeps this a fast unit test
// (no real copilot CLI spawn) and records the resume routing. [#1]
func TestResumeCopilotSessionReportsRichKind(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	repo := initTempRepo(t)

	h := newHost(io.Discard, time.Minute)
	// Resume routes through the rich SDK backend, not the ConPTY factory, so stub
	// startSDKSession: record the resume flag/session id and register a fake live
	// session under the workspace name (no real copilot CLI).
	var gotResume bool
	var gotSessionID, gotName string
	h.startSDKSessionHook = func(name, program, workDir, baseDir string, autoYes bool, sessionID, model, effort, contextTier string, resume bool) error {
		gotResume = resume
		gotSessionID = sessionID
		gotName = name
		h.mu.Lock()
		h.sessions[name] = &fakeSession{name: name, program: program, workDir: workDir, autoYes: autoYes, aliveFlag: true}
		h.mu.Unlock()
		return nil
	}
	m := h.workspaces

	const validID = "123e4567-e89b-12d3-a456-426614174000"
	resp := m.resumeCopilotSession(&proto.Request{
		ID:        1,
		SessionID: validID,
		RepoPath:  repo,
		// The test cwd is not the temp repo, so this is a cross-repo resume; the
		// host requires explicit confirmation before creating the worktree.
		Confirmed: true,
		Title:     "Resumed",
	})
	if !resp.OK {
		t.Fatalf("resumeCopilotSession failed: error=%q needsConfirm=%v", resp.Error, resp.NeedsConfirm)
	}
	if resp.Workspace == nil {
		t.Fatalf("resumeCopilotSession returned OK with no workspace")
	}
	if got := resp.Workspace.Kind; got != proto.WorkspaceKindRich {
		t.Fatalf("resumed workspace Kind = %q, want %q", got, proto.WorkspaceKindRich)
	}
	// The bug was the live session being terminal, not just the metadata tag: assert
	// the resume actually went through the SDK backend with resume=true.
	if !gotResume {
		t.Fatalf("resume did not route through the SDK backend with resume=true")
	}
	if gotSessionID != validID {
		t.Fatalf("SDK resume session id = %q, want %q", gotSessionID, validID)
	}
	if gotName != resp.Workspace.SessionName {
		t.Fatalf("SDK session name = %q, want workspace session name %q", gotName, resp.Workspace.SessionName)
	}
}

// runCopilotProbe runs copilotProbeScript with funcDef prepended (defining the
// agent named by HANGAR_PROBE_NAME) under -NoProfile, so the create-time
// detection heuristic is exercised hermetically. Returns the probe exit code
// (0 = copilot-backed, 1 = found but not copilot, 2 = not found).
func runCopilotProbe(t *testing.T, funcDef, name string) int {
	t.Helper()
	if _, err := exec.LookPath("powershell.exe"); err != nil {
		t.Skip("powershell.exe not available")
	}
	cmd := exec.Command("powershell.exe", "-NoProfile", "-WindowStyle", "Hidden", "-Command", funcDef+"\n"+copilotProbeScript)
	cmd.Env = append(os.Environ(), "HANGAR_PROBE_NAME="+name)
	if err := cmd.Run(); err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return ee.ExitCode()
		}
		t.Fatalf("probe launch failed: %v", err)
	}
	return 0
}

// TestCopilotProbeScriptDetection verifies the create-time probe recognises a
// copilot wrapper (e.g. `cpa`) via the parsed AST while ignoring comments and
// non-copilot agents — the heuristic that lets a wrapper get a resumable session.
func TestCopilotProbeScriptDetection(t *testing.T) {
	cases := []struct {
		name    string
		funcDef string
		probe   string
		want    int
	}{
		{"copilot wrapper", "function cpa { copilot --allow-all-tools --yolo @args }", "cpa", 0},
		{"copilot exe wrapper", "function cpa { copilot.exe @args }", "cpa", 0},
		{"non-copilot wrapper", "function claudewrap { claude @args }", "claudewrap", 1},
		{"copilot only in comment", "function claudewrap {\n claude @args # copilot\n }", "claudewrap", 1},
		{"missing command", "", "definitely_missing_agent_xyz", 2},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := runCopilotProbe(t, c.funcDef, c.probe); got != c.want {
				t.Fatalf("probe exit = %d, want %d", got, c.want)
			}
		})
	}
}

// TestClassifyProbeExit verifies the probe exit-code -> (found, isCopilot) mapping
// without spawning PowerShell: exit 0 = found+copilot, 1 = found, 2/other/launch
// failure = not found.
func TestClassifyProbeExit(t *testing.T) {
	if f, c := classifyProbeExit(nil); !f || !c {
		t.Fatalf("nil err: got (found=%v,isCopilot=%v), want (true,true)", f, c)
	}
	for _, tc := range []struct {
		code             int
		found, isCopilot bool
	}{
		{1, true, false},
		{2, false, false},
		{3, false, false}, // unknown exit code => not found
	} {
		err := exec.Command("cmd", "/c", fmt.Sprintf("exit %d", tc.code)).Run()
		if f, c := classifyProbeExit(err); f != tc.found || c != tc.isCopilot {
			t.Fatalf("exit %d: got (found=%v,isCopilot=%v), want (%v,%v)", tc.code, f, c, tc.found, tc.isCopilot)
		}
	}
	if f, c := classifyProbeExit(errors.New("launch failure")); f || c {
		t.Fatalf("non-exit err: got (found=%v,isCopilot=%v), want (false,false)", f, c)
	}
}

// TestProbeAgentProgramTimeout verifies the probe is bounded: with a 1ms deadline
// (far shorter than any PowerShell cold start) the probe is killed and reported as
// timed out + not found, instead of hanging CreateWorkspace forever.
func TestProbeAgentProgramTimeout(t *testing.T) {
	if _, err := exec.LookPath("powershell.exe"); err != nil {
		t.Skip("powershell.exe not available")
	}
	orig := agentProbeTimeout
	agentProbeTimeout = time.Millisecond
	t.Cleanup(func() { agentProbeTimeout = orig })

	found, isCopilot, timedOut := probeAgentProgramTimed("powershell", "cpa")
	if !timedOut {
		t.Fatalf("expected probe to time out; got found=%v isCopilot=%v timedOut=%v", found, isCopilot, timedOut)
	}
	if found || isCopilot {
		t.Fatalf("timed-out probe must report not found: found=%v isCopilot=%v", found, isCopilot)
	}
}

// TestCopilotResumable covers the name-independent resume gate: a detected wrapper
// (CopilotResume=true) and legacy literal-copilot workspaces both resume, while a
// wrapper without the persisted flag does not.
func TestCopilotResumable(t *testing.T) {
	cases := []struct {
		name string
		w    workspace
		want bool
	}{
		{"detected wrapper", workspace{Program: "cpa", CopilotResume: true}, true},
		{"legacy copilot", workspace{Program: "copilot"}, true},
		{"undetected wrapper", workspace{Program: "cpa"}, false},
		{"non-copilot", workspace{Program: "claude", CopilotResume: false}, false},
	}
	for _, c := range cases {
		if got := c.w.copilotResumable(); got != c.want {
			t.Fatalf("%s: copilotResumable() = %v, want %v", c.name, got, c.want)
		}
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
	authClient(t, c)

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
		`C:\dev\hangar`:    "hangar",
		`C:\dev\hangar\`:   "hangar",
		`D:\repos\my-app\`: "my-app",
		"":                 "workspace",
	}
	for in, want := range cases {
		if got := defaultTitle(in); got != want {
			t.Errorf("defaultTitle(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestDiffCachePruneAndKeepOnError verifies the background diff refresher prunes
// cache entries for workspaces that no longer exist, and keeps the previous
// counts (rather than flapping to 0) when a workspace's git diff errors/times out.
func TestDiffCachePruneAndKeepOnError(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	h := newHost(io.Discard, time.Minute)
	m := h.workspaces

	// A live workspace whose worktree path does not exist, so DiffNumstatTimeout
	// errors immediately and the refresher must keep its previously cached value.
	m.mu.Lock()
	m.wss["live"] = &workspace{
		ID:           "live",
		WorktreePath: filepath.Join(home, "does-not-exist"),
		BaseSHA:      "0000000000000000000000000000000000000000",
	}
	m.mu.Unlock()
	m.diffMu.Lock()
	m.diffCache["live"] = cachedDiff{added: 5, removed: 3}
	m.diffCache["stale"] = cachedDiff{added: 9, removed: 9} // no longer in wss
	m.diffMu.Unlock()

	m.refreshAllDiffs()

	if a, r := m.cachedDiffFor("live"); a != 5 || r != 3 {
		t.Fatalf("live diff should be kept on git error, got (%d,%d) want (5,3)", a, r)
	}
	m.diffMu.Lock()
	_, staleStillCached := m.diffCache["stale"]
	m.diffMu.Unlock()
	if staleStillCached {
		t.Fatal("stale cache entry should have been pruned")
	}
}

// TestToInfoMapsLastOutput verifies toInfo converts a live session's
// lastOutputUnixMs (UnixMilli) into WorkspaceInfo.LastOutputUnix (Unix seconds)
// via integer ms->s division, and reports 0 when there is no live session.
func TestToInfoMapsLastOutput(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	h := newHost(io.Discard, time.Minute)
	m := h.workspaces

	const sessName = "ws_last_output"
	const lastMs = int64(1_710_000_123_456)
	h.mu.Lock()
	h.sessions[sessName] = &fakeSession{name: sessName, aliveFlag: true, lastOutputMs: lastMs}
	h.mu.Unlock()

	if got, want := m.toInfo(&workspace{ID: "ws1", SessionName: sessName}, false, "").LastOutputUnix, lastMs/1000; got != want {
		t.Fatalf("LastOutputUnix = %d, want %d", got, want)
	}

	// No live session for this workspace -> 0 (unknown).
	if got := m.toInfo(&workspace{ID: "ws2", SessionName: "missing"}, false, "").LastOutputUnix; got != 0 {
		t.Fatalf("LastOutputUnix (no session) = %d, want 0", got)
	}
}

// TestInPlaceGitInfo verifies the in-place folder probe: a git repo yields its
// toplevel, current branch, and HEAD SHA; a plain folder yields all-empty so the
// caller opens the session with git features disabled.
func TestInPlaceGitInfo(t *testing.T) {
	repo := initTempRepo(t)
	repoPath, branch, baseSHA := inPlaceGitInfo(repo)
	if repoPath == "" {
		t.Fatalf("expected a repoPath for a git repo, got empty")
	}
	if branch != "main" {
		t.Fatalf("branch = %q, want main", branch)
	}
	if len(baseSHA) != 40 {
		t.Fatalf("baseSHA = %q, want a 40-char sha", baseSHA)
	}

	plain := t.TempDir()
	if rp, br, sha := inPlaceGitInfo(plain); rp != "" || br != "" || sha != "" {
		t.Fatalf("non-git folder should yield empty git info, got (%q,%q,%q)", rp, br, sha)
	}
}

// TestToInfoHasWorktree verifies the sidebar signal: worktree-backed workspaces
// report HasWorktree=true and in-place ones report false.
func TestToInfoHasWorktree(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	h := newHost(io.Discard, time.Minute)
	m := h.workspaces

	if !m.toInfo(&workspace{ID: "w1", SessionName: "s1"}, false, "").HasWorktree {
		t.Fatalf("worktree-backed workspace should report HasWorktree=true")
	}
	if m.toInfo(&workspace{ID: "w2", SessionName: "s2", NoWorktree: true}, false, "").HasWorktree {
		t.Fatalf("in-place workspace should report HasWorktree=false")
	}
}

// TestLoadKeepsInPlaceSkipsUncontained guards the load() fix: an in-place
// workspace whose path is (correctly) outside the managed worktrees dir must
// survive a host restart, while a worktree-backed workspace with an uncontained
// path is still rejected as unsafe (F-09).
func TestLoadKeepsInPlaceSkipsUncontained(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	outside := t.TempDir() // not under the managed worktrees dir

	// saveLocked silently no-ops if the config dir is missing, so create it.
	p, err := workspacesPath()
	if err != nil {
		t.Fatalf("workspacesPath: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}

	h1 := newHost(io.Discard, time.Minute)
	m1 := h1.workspaces
	m1.mu.Lock()
	m1.wss = map[string]*workspace{
		"inplace": {ID: "inplace", SessionName: "ws_inplace", NoWorktree: true, WorktreePath: outside},
		"bad":     {ID: "bad", SessionName: "ws_bad", WorktreePath: outside}, // worktree-backed + uncontained
	}
	m1.saveLocked()
	m1.mu.Unlock()

	// A fresh manager loads from the same workspaces.json.
	h2 := newHost(io.Discard, time.Minute)
	m2 := h2.workspaces
	m2.mu.Lock()
	_, hasInplace := m2.wss["inplace"]
	_, hasBad := m2.wss["bad"]
	n := len(m2.wss)
	m2.mu.Unlock()

	if !hasInplace {
		t.Fatalf("in-place workspace with uncontained path should be kept on load")
	}
	if hasBad {
		t.Fatalf("worktree-backed workspace with uncontained path should be skipped on load")
	}
	if n != 1 {
		t.Fatalf("expected exactly 1 workspace loaded, got %d", n)
	}
}

// TestRichModelSelectionPersistsAndReloads proves a rich session's model selection
// (v18) survives a daemon restart: setRichModelSelection stores Model/ReasoningEffort/
// ContextTier on the workspace by session name and saves, and a fresh manager
// reloads them from workspaces.json so reviveBySession can restore the selection.
func TestRichModelSelectionPersistsAndReloads(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	// saveLocked silently no-ops if the config dir is missing, so create it.
	p, err := workspacesPath()
	if err != nil {
		t.Fatalf("workspacesPath: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}

	h1 := newHost(io.Discard, time.Minute)
	m1 := h1.workspaces
	m1.mu.Lock()
	m1.wss = map[string]*workspace{
		"rich1": {ID: "rich1", SessionName: "ws_rich1", Kind: "rich", NoWorktree: true},
	}
	m1.saveLocked()
	m1.mu.Unlock()

	// The SetModel persistence path: store a selection keyed by session name.
	m1.setRichModelSelection("ws_rich1", "gpt-5", "high", "long_context")

	// A fresh manager reloads from the same workspaces.json.
	h2 := newHost(io.Discard, time.Minute)
	m2 := h2.workspaces
	m2.mu.Lock()
	w := m2.wss["rich1"]
	m2.mu.Unlock()
	if w == nil {
		t.Fatal("rich workspace not reloaded")
	}
	if w.Model != "gpt-5" || w.ReasoningEffort != "high" || w.ContextTier != "long_context" {
		t.Fatalf("reloaded selection = %q/%q/%q, want gpt-5/high/long_context", w.Model, w.ReasoningEffort, w.ContextTier)
	}
}

// TestSetRichModelSelectionUnknownSessionNoop proves setRichModelSelection is a
// no-op (and does not panic) when no workspace owns the named session.
func TestSetRichModelSelectionUnknownSessionNoop(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	h := newHost(io.Discard, time.Minute)
	h.workspaces.mu.Lock()
	h.workspaces.wss = map[string]*workspace{
		"rich1": {ID: "rich1", SessionName: "ws_rich1", Kind: "rich", NoWorktree: true},
	}
	h.workspaces.mu.Unlock()

	h.workspaces.setRichModelSelection("ghost", "gpt-5", "high", "long_context") // must not panic

	h.workspaces.mu.Lock()
	w := h.workspaces.wss["rich1"]
	h.workspaces.mu.Unlock()
	if w.Model != "" || w.ReasoningEffort != "" || w.ContextTier != "" {
		t.Fatalf("unrelated workspace mutated: %q/%q/%q", w.Model, w.ReasoningEffort, w.ContextTier)
	}
}

// TestArchiveInPlaceLeavesFolder guards the archive safety rule: archiving an
// in-place session must never delete the user's folder or branch, even when the
// request sets DeleteWorktree.
func TestArchiveInPlaceLeavesFolder(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	repo := initTempRepo(t) // a real folder we must NOT delete
	marker := filepath.Join(repo, "README.md")

	h := newHost(io.Discard, time.Minute)
	m := h.workspaces
	m.mu.Lock()
	m.wss["ip"] = &workspace{ID: "ip", SessionName: "ws_ip", NoWorktree: true, WorktreePath: repo, RepoPath: repo, Branch: "main"}
	m.mu.Unlock()

	resp := m.archive(&proto.Request{ID: 1, WorkspaceID: "ip", DeleteWorktree: true})
	if resp == nil || !resp.OK {
		t.Fatalf("archive failed: %+v", resp)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("in-place folder must be left intact after archive, stat err=%v", err)
	}
	m.mu.Lock()
	_, still := m.wss["ip"]
	m.mu.Unlock()
	if still {
		t.Fatalf("workspace should be removed from the registry after archive")
	}
}
