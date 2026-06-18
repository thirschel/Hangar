//go:build windows

package winhost

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"syscall"

	"golang.org/x/sys/windows"
)

// currentUserSID returns the current process user's SID string (e.g. S-1-5-21-...).
func currentUserSID() (string, error) {
	tok, err := windows.OpenCurrentProcessToken()
	if err != nil {
		return "", err
	}
	defer tok.Close()
	u, err := tok.GetTokenUser()
	if err != nil {
		return "", err
	}
	return u.User.Sid.String(), nil
}

// currentUserSDDL grants the current user full access only (protected DACL).
func currentUserSDDL() (string, error) {
	sid, err := currentUserSID()
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("D:P(A;;GA;;;%s)", sid), nil
}

// controlPipeName returns the per-user control pipe path. It is stable per user
// so a client can always find the host.
func controlPipeName() (string, error) {
	sid, err := currentUserSID()
	if err != nil {
		return "", err
	}
	return `\\.\pipe\hangar-host-` + sid, nil
}

// acquireLock opens (creating if needed) the lock file and takes an exclusive,
// non-blocking lock for the lifetime of the returned file. The host owns this
// lock for its whole run; a second host fails here and exits, guaranteeing a
// single host per user.
func acquireLock(path string) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	ol := new(windows.Overlapped)
	err = windows.LockFileEx(windows.Handle(f.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY,
		0, 1, 0, ol)
	if err != nil {
		f.Close()
		return nil, fmt.Errorf("another session-host holds the lock: %w", err)
	}
	return f, nil
}

func releaseLock(f *os.File) {
	if f == nil {
		return
	}
	ol := new(windows.Overlapped)
	_ = windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 1, 0, ol)
	_ = f.Close()
}

func writeHostInfo(identity *hostIdentity) error {
	p, err := hostInfoPath()
	if err != nil {
		return err
	}
	b, err := json.MarshalIndent(identity.hostInfo(), "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(p, b, 0o600)
}

func removeHostInfo() {
	if p, err := hostInfoPath(); err == nil {
		_ = os.Remove(p)
	}
}

// spawnDetachedHost starts a new `cs --session-host` process fully detached from
// the current process: no inherited console, its own process group, no std I/O.
// It keeps running after the spawning TUI exits.
func spawnDetachedHost() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	cmd := exec.Command(exe, SessionHostCmd)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: windows.DETACHED_PROCESS | windows.CREATE_NEW_PROCESS_GROUP,
	}
	cmd.Stdin, cmd.Stdout, cmd.Stderr = nil, nil, nil
	if err := cmd.Start(); err != nil {
		return err
	}
	// Release the child; it lives independently.
	return cmd.Process.Release()
}
