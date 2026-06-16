//go:build !windows

package git

import "os/exec"

// hideConsole is a no-op off Windows, where there is no console window to hide.
func hideConsole(cmd *exec.Cmd) {}
