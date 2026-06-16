//go:build windows

package git

import (
	"os/exec"
	"syscall"

	"golang.org/x/sys/windows"
)

// hideConsole makes a console subprocess run without allocating or showing a
// console window. The session-host daemon runs console-less (it is spawned
// detached), so any console child it launches — git, gh, where — would otherwise
// pop a brief visible window on every invocation. That is especially noticeable
// because the desktop app polls the diff every couple of seconds, which runs
// `git diff` per workspace; without this the user sees a window flashing
// constantly. CREATE_NO_WINDOW only suppresses the window — captured stdout/
// stderr pipes (Output/CombinedOutput) keep working normally.
func hideConsole(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.HideWindow = true
	cmd.SysProcAttr.CreationFlags |= windows.CREATE_NO_WINDOW
}
