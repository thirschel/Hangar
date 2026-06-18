//go:build !windows

package daemon

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

// getSysProcAttr returns platform-specific process attributes for detaching the child process
func getSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{
		Setsid: true, // Create a new session
	}
}

// isDaemonProcess returns true if the process at pid is a Hangar daemon binary
// (cs or hangar).  Returns (false, nil) when the process is gone
// or is not a Hangar binary.  Returns (false, err) only for unexpected errors.
func isDaemonProcess(pid int) (bool, error) {
	// On Linux, /proc is available and gives us a reliable exe path.
	if _, err := os.Stat("/proc"); err == nil {
		return isDaemonProcessLinux(pid)
	}
	// On macOS/FreeBSD (no /proc), fall back to ps(1).
	return isDaemonProcessPS(pid)
}

// isDaemonProcessLinux reads the /proc/<pid>/exe symlink on Linux.
func isDaemonProcessLinux(pid int) (bool, error) {
	exePath, err := os.Readlink(fmt.Sprintf("/proc/%d/exe", pid))
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil // process gone
		}
		return false, err
	}
	base := strings.ToLower(filepath.Base(exePath))
	return base == "cs" || base == "hangar", nil
}

// isDaemonProcessPS uses ps(1) to look up the process name — works on macOS and
// any Unix that ships a POSIX ps.
func isDaemonProcessPS(pid int) (bool, error) {
	out, err := exec.Command("ps", "-p", fmt.Sprintf("%d", pid), "-o", "comm=").Output()
	if err != nil {
		// exit code 1 means the PID does not exist.
		return false, nil
	}
	base := strings.ToLower(strings.TrimSpace(string(out)))
	return base == "cs" || base == "hangar", nil
}
