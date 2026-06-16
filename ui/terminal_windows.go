//go:build windows

package ui

// terminalTabSupported is false on native Windows: the Terminal tab is a
// tmux/POSIX-shell feature that is not ported to the session-host backend yet.
// The pane shows an explanatory message instead.
const terminalTabSupported = false
