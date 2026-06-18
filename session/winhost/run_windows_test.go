//go:build windows

package winhost

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestLoadDoesNotAutoFireRunCommand asserts that a persisted RunCommand is never
// executed on load/revive — it only runs from an explicit, user-initiated
// StartRun. This is the proportionate containment for F-33 (a tampered
// workspaces.json must not silently re-fire `cmd.exe /c <command>`).
func TestLoadDoesNotAutoFireRunCommand(t *testing.T) {
	testHome(t)

	p, err := workspacesPath()
	if err != nil {
		t.Fatalf("workspacesPath: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	// worktreePath is left empty here: this test isolates F-33 (a persisted
	// RunCommand must not auto-fire on load). Worktree-path containment on load
	// is covered separately; an empty path skips that unrelated check.
	const data = `[{"id":"w1","sessionName":"ws_w1","runCommand":"calc.exe","worktreePath":""}]`
	if err := os.WriteFile(p, []byte(data), 0o600); err != nil {
		t.Fatalf("write workspaces.json: %v", err)
	}

	// Constructing the host loads persisted workspaces.
	h := newHost(io.Discard, time.Minute)

	h.workspaces.mu.Lock()
	w, ok := h.workspaces.wss["w1"]
	h.workspaces.mu.Unlock()
	if !ok || w.RunCommand != "calc.exe" {
		t.Fatalf("expected workspace w1 with persisted RunCommand, got %+v", w)
	}
	if running, _ := h.runs.info("w1"); running {
		t.Fatal("load() must not auto-fire a persisted RunCommand (F-33)")
	}
	if h.runs.existingState("w1") != nil {
		t.Fatal("load() must not create a run state for a persisted RunCommand (F-33)")
	}
}

func TestRunLifecycle(t *testing.T) {
	requireConPTY(t)
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

	ws, err := c.CreateWorkspace(repo, "Run Test", "cmd", "")
	if err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}
	defer func() { _ = c.ArchiveWorkspace(ws.ID, true) }()

	const command = `echo http://localhost:5173 & ping -n 12 127.0.0.1 >NUL`
	if err := c.StartRun(ws.ID, command); err != nil {
		t.Fatalf("StartRun: %v", err)
	}

	var (
		offset     int64
		accum      strings.Builder
		sawRunning bool
	)
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		data, next, running, _, err := c.WorkspaceRunOutput(ws.ID, offset)
		if err != nil {
			t.Fatalf("WorkspaceRunOutput: %v", err)
		}
		offset = next
		if running {
			sawRunning = true
		}
		accum.Write(data)
		if strings.Contains(accum.String(), "localhost:5173") {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !strings.Contains(accum.String(), "localhost:5173") {
		t.Fatalf("expected run output to contain preview URL, got %q", accum.String())
	}
	if !sawRunning {
		t.Fatalf("expected run to report running before stop")
	}

	list, err := c.ListWorkspaces()
	if err != nil {
		t.Fatalf("ListWorkspaces: %v", err)
	}
	if len(list) != 1 || list[0].ID != ws.ID {
		t.Fatalf("expected one workspace %s, got %+v", ws.ID, list)
	}
	if list[0].PreviewURL != "http://localhost:5173" {
		t.Fatalf("PreviewURL = %q, want %q", list[0].PreviewURL, "http://localhost:5173")
	}
	if !list[0].Running {
		t.Fatalf("expected workspace to report running before stop")
	}
	if list[0].RunCommand != command {
		t.Fatalf("RunCommand = %q, want %q", list[0].RunCommand, command)
	}

	if err := c.StopRun(ws.ID); err != nil {
		t.Fatalf("StopRun: %v", err)
	}
	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		_, next, running, _, err := c.WorkspaceRunOutput(ws.ID, offset)
		if err != nil {
			t.Fatalf("WorkspaceRunOutput after stop: %v", err)
		}
		offset = next
		if !running {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("expected run to stop within timeout")
}
