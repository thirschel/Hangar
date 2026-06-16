//go:build windows

package git

import (
	"os/exec"
	"testing"

	"golang.org/x/sys/windows"
)

// TestHideConsoleSetsCreateNoWindow guards the core mechanism that keeps the
// detached daemon from flashing a console window on every git/gh/where call
// (notably the diff polling that runs every couple of seconds).
func TestHideConsoleSetsCreateNoWindow(t *testing.T) {
	cmd := exec.Command("git", "version")
	hideConsole(cmd)
	if cmd.SysProcAttr == nil {
		t.Fatal("SysProcAttr not set")
	}
	if cmd.SysProcAttr.CreationFlags&windows.CREATE_NO_WINDOW == 0 {
		t.Fatalf("CREATE_NO_WINDOW not set, flags=0x%x", cmd.SysProcAttr.CreationFlags)
	}
	if !cmd.SysProcAttr.HideWindow {
		t.Fatal("HideWindow not set")
	}
}
