//go:build !windows

package winhost

import "errors"

// RunHost is a stub on non-Windows platforms: the native session host is a
// Windows-only construct (Unix uses tmux). It exists so main.go can register the
// hidden subcommand unconditionally.
func RunHost() error {
	return errors.New("the native session host is only supported on Windows; use tmux on this platform")
}
