//go:build windows

package daemon

import (
	"path/filepath"
	"strings"
	"syscall"

	"golang.org/x/sys/windows"
)

// getSysProcAttr returns platform-specific process attributes for detaching the child process
func getSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{
		CreationFlags: windows.CREATE_NEW_PROCESS_GROUP | windows.DETACHED_PROCESS,
	}
}

// isDaemonProcess returns true if the process at pid is a Hangar daemon binary
// (cs.exe or hangar.exe).
// Returns (false, nil) when the process is gone, belongs to another user, or is
// not a Hangar binary.  Returns (false, err) only for unexpected system errors.
func isDaemonProcess(pid int) (bool, error) {
	const processQueryLimitedInformation = 0x1000
	h, err := windows.OpenProcess(processQueryLimitedInformation, false, uint32(pid))
	if err != nil {
		// ERROR_INVALID_PARAMETER: PID does not exist.
		// ERROR_ACCESS_DENIED: process belongs to another user — not our daemon.
		if err == windows.ERROR_INVALID_PARAMETER || err == windows.ERROR_ACCESS_DENIED {
			return false, nil
		}
		return false, err
	}
	defer windows.CloseHandle(h)

	buf := make([]uint16, windows.MAX_PATH)
	size := uint32(len(buf))
	if err := windows.QueryFullProcessImageName(h, 0, &buf[0], &size); err != nil {
		return false, err
	}
	exePath := windows.UTF16ToString(buf[:size])
	base := strings.ToLower(filepath.Base(exePath))
	return base == "cs.exe" || base == "hangar.exe", nil
}
