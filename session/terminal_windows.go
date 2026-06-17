//go:build windows

package session

import (
	"hangar/cmd"
	"hangar/session/winhost"
)

// NewTerminalSession creates a new terminal session for the current platform.
// On Windows, this returns a native session-host-backed session (ConPTY + VT
// emulator), which persists across TUI restarts like tmux does on Unix.
func NewTerminalSession(name, program string) TerminalSession {
	return winhost.NewSession(name, program)
}

// NewTerminalSessionWithDeps creates a session for testing. The Windows backend
// has no injectable dependencies (it talks to the host over a pipe), so the
// executor is ignored.
func NewTerminalSessionWithDeps(name, program string, _ cmd.Executor) TerminalSession {
	return winhost.NewSession(name, program)
}

// CleanupTerminalSessions stops the session host (killing all sessions). Used by
// `cs reset`.
func CleanupTerminalSessions(_ cmd.Executor) error {
	return winhost.Shutdown()
}
