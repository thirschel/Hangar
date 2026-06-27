package proto

import (
	"encoding/json"
	"testing"
)

func TestVersionV19(t *testing.T) {
	if Version < 19 {
		t.Fatalf("Version = %d, want >= 19", Version)
	}
}

// TestV19ReasoningDeltaKind guards the new rich-stream Kind the desktop matches on to
// grow the live "thinking" block: each incremental reasoning chunk arrives as an
// assistant.reasoning.delta frame, while the complete block still arrives as the
// existing assistant.reasoning frame (the finalizer).
func TestV19ReasoningDeltaKind(t *testing.T) {
	if EventKindReasoningDelta != "assistant.reasoning.delta" {
		t.Fatalf("EventKindReasoningDelta = %q, want %q", EventKindReasoningDelta, "assistant.reasoning.delta")
	}
	if EventKindReasoning != "assistant.reasoning" {
		t.Fatalf("EventKindReasoning = %q, want %q", EventKindReasoning, "assistant.reasoning")
	}
}

// TestReasoningDeltaRoundTrip guards the v19 wire shape for an assistant.reasoning.delta
// frame: the incremental chunk rides in the existing Text field (under the "text" JSON
// key) and the frame adds NO new fields, so it stays byte-compatible with a v18 reader.
func TestReasoningDeltaRoundTrip(t *testing.T) {
	frame := EventFrame{Seq: 5, Kind: EventKindReasoningDelta, Text: "thin"}
	b, err := json.Marshal(frame)
	if err != nil {
		t.Fatalf("Marshal assistant.reasoning.delta: %v", err)
	}
	var got EventFrame
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("Unmarshal assistant.reasoning.delta: %v", err)
	}
	if got.Kind != EventKindReasoningDelta || got.Text != "thin" {
		t.Fatalf("assistant.reasoning.delta round-trip = %+v", got)
	}
	if !containsKey(b, "text") {
		t.Fatalf("expected %q key on an assistant.reasoning.delta frame, got %s", "text", b)
	}
	// The frozen v19 contract adds NO new EventFrame fields, so the only populated keys
	// are the existing seq/kind/text.
	var m map[string]json.RawMessage
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("Unmarshal to map: %v", err)
	}
	for key := range m {
		switch key {
		case "seq", "kind", "text":
		default:
			t.Fatalf("unexpected key %q on an assistant.reasoning.delta frame: %s", key, b)
		}
	}
}
