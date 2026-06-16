//go:build windows

package winhost

import (
	"os/exec"
	"syscall"

	"golang.org/x/sys/windows"
)

// hideConsole makes a console subprocess run without allocating or showing a
// console window. The daemon runs console-less (spawned detached), so any
// console child it launches — e.g. the per-workspace `git` used for diff stats
// and commit/push — would otherwise flash a brief window on every call. The
// diff path runs on the app's couple-second poll, so without this the user sees
// a window flashing constantly. CREATE_NO_WINDOW only suppresses the window;
// captured stdout/stderr pipes keep working.
func hideConsole(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.HideWindow = true
	cmd.SysProcAttr.CreationFlags |= windows.CREATE_NO_WINDOW
}
