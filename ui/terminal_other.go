//go:build !windows

package ui

// terminalTabSupported reports whether the interactive Terminal tab is available
// on this platform. It is backed by tmux on Unix.
const terminalTabSupported = true
