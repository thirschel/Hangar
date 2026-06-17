package daemon

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestIsDaemonProcessRejectsNonDaemon verifies that the process identity check
// correctly identifies the current test process as *not* a Hangar daemon binary.
//
// The test runner binary is named something like "daemon.test[.exe]", which is
// not in the allowed set {cs, claude-squad, hangar} (or their .exe variants).
// This guards against a blind Kill of a process that recycled a stale daemon.pid
// (F-31).
func TestIsDaemonProcessRejectsNonDaemon(t *testing.T) {
	pid := os.Getpid()
	ok, err := isDaemonProcess(pid)
	require.NoError(t, err, "isDaemonProcess must not return an error for the current process")
	require.False(t, ok, "test binary must not be identified as a Hangar daemon (got true for pid %d)", pid)
}

// TestIsDaemonProcessRejectsNonexistentPID verifies that a PID that is very
// unlikely to exist is handled without error.
func TestIsDaemonProcessRejectsNonexistentPID(t *testing.T) {
	// PID 0 is never a valid user process.
	ok, err := isDaemonProcess(0)
	// Either (false, nil) or (false, <some error>) is acceptable — the important
	// property is that ok must be false so we never kill PID 0.
	if err != nil {
		t.Logf("isDaemonProcess(0) returned err (acceptable): %v", err)
	}
	require.False(t, ok, "PID 0 must never be identified as a Hangar daemon")
}
