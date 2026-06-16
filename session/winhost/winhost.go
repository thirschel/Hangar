package winhost

import "claude-squad/session/winhost/proto"

// SessionHostCmd is the hidden cobra subcommand that runs the native Windows
// session host: `cs session-host`. It is referenced by main.go (cross-platform)
// and by the detached spawn on Windows.
const SessionHostCmd = "session-host"

func hostProtocolVersion() int { return proto.Version }
