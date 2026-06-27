package proto

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"testing"
)

// desktopProtoVersionFile is the desktop TypeScript module that mirrors the
// proto.Version constant. The desktop named-pipe client compares its
// PROTO_VERSION against the host-reported version during the Hello handshake,
// so the two MUST stay in lockstep. The path is relative to this package
// directory (session/winhost/proto), which is the working directory for
// `go test`; filepath.Join keeps it correct on linux CI as well.
var desktopProtoVersionFile = filepath.Join("..", "..", "..", "desktop", "src", "shared", "proto-version.ts")

// protoVersionRE extracts the integer from a line such as
// `export const PROTO_VERSION = 12;`. The `export` keyword and the surrounding
// whitespace are both optional.
var protoVersionRE = regexp.MustCompile(`PROTO_VERSION\s*=\s*(\d+)`)

// TestProtoVersionMatchesDesktop guards against the Go (proto.Version) and the
// TypeScript (PROTO_VERSION) protocol-version constants drifting apart. A
// mismatch ships a desktop client that fails the version handshake against a
// host running the other version, and nothing else in the suite cross-checks
// the two, so this test fails loudly the moment one side is bumped without the
// other.
func TestProtoVersionMatchesDesktop(t *testing.T) {
	data, err := os.ReadFile(desktopProtoVersionFile)
	if err != nil {
		t.Fatalf("read desktop proto version file %s: %v (did desktop/src/shared/proto-version.ts move or get renamed?)", desktopProtoVersionFile, err)
	}

	m := protoVersionRE.FindSubmatch(data)
	if m == nil {
		t.Fatalf("could not find a PROTO_VERSION assignment in %s (expected a line like `export const PROTO_VERSION = %d;`)", desktopProtoVersionFile, Version)
	}

	desktopVersion, err := strconv.Atoi(string(m[1]))
	if err != nil {
		t.Fatalf("parse PROTO_VERSION %q from %s: %v", m[1], desktopProtoVersionFile, err)
	}

	if desktopVersion != Version {
		t.Fatalf("proto version drift: session/winhost/proto/proto.go Version=%d but %s PROTO_VERSION=%d - bump them together", Version, desktopProtoVersionFile, desktopVersion)
	}
}
