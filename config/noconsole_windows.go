//go:build windows

package config

import (
	"os/exec"
	"syscall"

	"golang.org/x/sys/windows"
)

// hideConsole makes a console subprocess run without allocating or showing a
// console window, so a console-less caller (e.g. the detached session-host
// daemon) doesn't flash a window when probing for the agent via `where`.
func hideConsole(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.HideWindow = true
	cmd.SysProcAttr.CreationFlags |= windows.CREATE_NO_WINDOW
}
