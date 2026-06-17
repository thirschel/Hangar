package winhost

import (
	"errors"
	"fmt"

	"hangar/session/winhost/proto"
)

// SessionHostCmd is the hidden cobra subcommand that runs the native Windows
// session host: `cs session-host`. It is referenced by main.go (cross-platform)
// and by the detached spawn on Windows.
const SessionHostCmd = "session-host"

// ErrSessionGone indicates the host no longer has the session (e.g. the host
// process died, such as across an OS reboot). Callers should recreate the
// session rather than treat this as fatal. It is defined here (a cross-platform
// file) so instance.go can reference it without importing Windows-only code.
var ErrSessionGone = errors.New("session host no longer has this session")

// VersionMismatch is returned by EnsureHost when a running host speaks a
// different protocol version (e.g. after upgrading cs while an old host is still
// running). The caller decides whether to restart it, since a restart destroys
// live sessions. Defined in this cross-platform file so the TUI startup can
// detect it via AsVersionMismatch without importing Windows-only code.
type VersionMismatch struct{ HostVersion, ClientVersion int }

func (e *VersionMismatch) Error() string {
	return fmt.Sprintf("session-host protocol mismatch: host=%d client=%d", e.HostVersion, e.ClientVersion)
}

// AsVersionMismatch reports whether err is (or wraps) a VersionMismatch.
func AsVersionMismatch(err error) (*VersionMismatch, bool) {
	var vm *VersionMismatch
	if errors.As(err, &vm) {
		return vm, true
	}
	return nil, false
}

func hostProtocolVersion() int { return proto.Version }
