package proto

import (
	"encoding/json"
	"testing"
)

func TestVersionV21(t *testing.T) {
	if Version < 21 {
		t.Fatalf("Version = %d, want >= 21", Version)
	}
}

// TestV21AicRoundTrip guards the v21 wire addition: EventFrame.Aic (the session's
// accumulated AI units, the CLI's "AIC used") rides under the "aic" JSON key on the
// usage frame. omitempty keeps a usage frame before any request byte-compatible with
// a v20 reader.
func TestV21AicRoundTrip(t *testing.T) {
	frame := EventFrame{Seq: 9, Kind: EventKindUsage, Model: "gpt-5", CurrentTokens: 100, TokenLimit: 1000, Aic: 11.32}
	b, err := json.Marshal(frame)
	if err != nil {
		t.Fatalf("Marshal usage: %v", err)
	}
	if !containsKey(b, "aic") {
		t.Fatalf("expected %q key on a usage frame with AIC, got %s", "aic", b)
	}
	var got EventFrame
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("Unmarshal usage: %v", err)
	}
	if got.Aic != 11.32 {
		t.Fatalf("Aic round-trip = %v, want %v", got.Aic, 11.32)
	}

	// omitempty: a usage frame before any request (Aic == 0) must NOT emit the key,
	// so it stays byte-compatible with a v20 reader.
	bare, err := json.Marshal(EventFrame{Seq: 10, Kind: EventKindUsage, CurrentTokens: 1, TokenLimit: 2})
	if err != nil {
		t.Fatalf("Marshal bare usage: %v", err)
	}
	if containsKey(bare, "aic") {
		t.Fatalf("did not expect %q key on a usage frame with zero AIC, got %s", "aic", bare)
	}
}
