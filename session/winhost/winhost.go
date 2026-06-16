package winhost

import (
	"errors"

	"claude-squad/session/winhost/proto"
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

func hostProtocolVersion() int { return proto.Version }
