//go:build !windows

package session

import (
	"hangar/cmd"
	"hangar/session/tmux"
)

// NewTerminalSession creates a new terminal session for the current platform.
// On Unix, this returns a tmux-backed session.
func NewTerminalSession(name, program string) TerminalSession {
	return tmux.NewTmuxSession(name, program)
}

// NewTerminalSessionWithDeps creates a new terminal session with provided dependencies for testing.
func NewTerminalSessionWithDeps(name, program string, ptyFactory tmux.PtyFactory, cmdExec cmd.Executor) TerminalSession {
	return tmux.NewTmuxSessionWithDeps(name, program, ptyFactory, cmdExec)
}

// CleanupTerminalSessions cleans up all terminal sessions created by hangar.
func CleanupTerminalSessions(cmdExec cmd.Executor) error {
	return tmux.CleanupSessions(cmdExec)
}
