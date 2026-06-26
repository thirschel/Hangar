package proto

import (
	"encoding/json"
	"testing"
)

func TestVersionV22(t *testing.T) {
	if Version < 22 {
		t.Fatalf("Version = %d, want >= 22", Version)
	}
}

// TestV22TimestampRoundTrip guards the v22 wire addition: EventFrame.Timestamp (the
// SDK event time in unix ms) rides under the "ts" JSON key on the idle frame so the
// desktop can show when a turn completed. omitempty keeps a frame without a time
// byte-compatible with a v21 reader.
func TestV22TimestampRoundTrip(t *testing.T) {
	frame := EventFrame{Seq: 11, Kind: EventKindIdle, Timestamp: 1717000000000}
	b, err := json.Marshal(frame)
	if err != nil {
		t.Fatalf("Marshal idle: %v", err)
	}
	if !containsKey(b, "ts") {
		t.Fatalf("expected %q key on an idle frame with a timestamp, got %s", "ts", b)
	}
	var got EventFrame
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("Unmarshal idle: %v", err)
	}
	if got.Timestamp != 1717000000000 {
		t.Fatalf("Timestamp round-trip = %d, want %d", got.Timestamp, int64(1717000000000))
	}

	// omitempty: a frame without a timestamp must NOT emit the key, so it stays
	// byte-compatible with a v21 reader.
	bare, err := json.Marshal(EventFrame{Seq: 12, Kind: EventKindIdle, Aborted: true})
	if err != nil {
		t.Fatalf("Marshal bare idle: %v", err)
	}
	if containsKey(bare, "ts") {
		t.Fatalf("did not expect %q key on an idle frame without a timestamp, got %s", "ts", bare)
	}
}
