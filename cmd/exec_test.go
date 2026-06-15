package cmd

import (
	"errors"
	"os/exec"
	"runtime"
	"strings"
	"testing"
)

// failingCmd returns a command that prints to stderr and exits non-zero on the
// current platform.
func failingCmd() *exec.Cmd {
	if runtime.GOOS == "windows" {
		return exec.Command("cmd", "/c", "echo boom 1>&2 & exit 1")
	}
	return exec.Command("sh", "-c", "echo boom 1>&2; exit 1")
}

func TestExecOutputIncludesStderr(t *testing.T) {
	_, err := Exec{}.Output(failingCmd())
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("expected stderr 'boom' surfaced in error, got %v", err)
	}
	// CleanupSessions relies on this still resolving to *exec.ExitError.
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("expected wrapped *exec.ExitError, got %T", err)
	}
}

func TestExecRunIncludesStderr(t *testing.T) {
	err := Exec{}.Run(failingCmd())
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("expected stderr 'boom' surfaced in error, got %v", err)
	}
}

func TestAppendStderrEmptyReturnsOriginal(t *testing.T) {
	base := errors.New("exit status 1")
	if got := appendStderr(base, []byte("   \n")); got != base {
		t.Fatalf("expected original error to be returned unchanged, got %v", got)
	}
}

func TestAppendStderrTrimsAndAppends(t *testing.T) {
	base := errors.New("exit status 1")
	got := appendStderr(base, []byte("  can't find pane: x\n"))
	if got.Error() != "exit status 1: can't find pane: x" {
		t.Fatalf("unexpected wrapped message: %q", got.Error())
	}
}
