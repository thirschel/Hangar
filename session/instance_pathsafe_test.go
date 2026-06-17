package session

import (
	cslog "hangar/log"
	"os"
	"runtime"
	"strings"
	"testing"
)

// TestMain initializes the global logger so session tests that exercise code
// paths which log (e.g. FromInstanceData rejecting tampered state) do not nil
// panic on log.ErrorLog. Mirrors the winhost package's TestMain.
func TestMain(m *testing.M) {
	cslog.Initialize(false)
	code := m.Run()
	cslog.Close()
	os.Exit(code)
}

// TestFromInstanceDataRejectsInjectedSHA ensures a tampered state.json base
// commit SHA (e.g. a git diff --output= injection) is rejected at the trust
// boundary before any GitWorktree is constructed (F-08).
func TestFromInstanceDataRejectsInjectedSHA(t *testing.T) {
	data := InstanceData{Title: "t", Branch: "b"}
	data.Worktree.BaseCommitSHA = `--output=C:\evil\dump`

	_, err := FromInstanceData(data)
	if err == nil {
		t.Fatalf("expected error for injected base SHA, got nil")
	}
	if !strings.Contains(err.Error(), "unsafe base commit SHA") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestFromInstanceDataRejectsUncontainedWorktree ensures a tampered worktree
// path pointing at a system directory is rejected before it can be handed to
// os.RemoveAll on a later Pause (F-09).
func TestFromInstanceDataRejectsUncontainedWorktree(t *testing.T) {
	evil := "/etc/cron.d"
	if runtime.GOOS == "windows" {
		evil = `C:\Windows\System32`
	}
	data := InstanceData{Title: "t", Branch: "b"}
	// Valid SHA so we reach the worktree-path check.
	data.Worktree.BaseCommitSHA = "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2"
	data.Worktree.WorktreePath = evil

	_, err := FromInstanceData(data)
	if err == nil {
		t.Fatalf("expected error for uncontained worktree path, got nil")
	}
	if !strings.Contains(err.Error(), "outside the managed worktrees") {
		t.Fatalf("unexpected error: %v", err)
	}
}
