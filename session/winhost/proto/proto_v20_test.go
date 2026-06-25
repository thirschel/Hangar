package proto

import (
	"encoding/json"
	"testing"
)

func TestVersionV20(t *testing.T) {
	if Version < 20 {
		t.Fatalf("Version = %d, want >= 20", Version)
	}
}

// TestV20ToolCallIDRoundTrip guards the v20 wire addition: EventFrame.ToolCallID
// (the SDK tool-call id) rides under the "toolCallId" JSON key on tool.start /
// tool.complete and on permission.requested, so the desktop can attach an AutoYes
// permission badge to the exact tool line. omitempty keeps a frame without an id
// byte-compatible with a v19 reader.
func TestV20ToolCallIDRoundTrip(t *testing.T) {
	frame := EventFrame{Seq: 7, Kind: EventKindToolStart, ToolCallID: "call-123", ToolName: "bash"}
	b, err := json.Marshal(frame)
	if err != nil {
		t.Fatalf("Marshal tool.start: %v", err)
	}
	if !containsKey(b, "toolCallId") {
		t.Fatalf("expected %q key on a tool.start frame, got %s", "toolCallId", b)
	}
	var got EventFrame
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("Unmarshal tool.start: %v", err)
	}
	if got.ToolCallID != "call-123" {
		t.Fatalf("ToolCallID round-trip = %q, want %q", got.ToolCallID, "call-123")
	}

	// omitempty: a frame without a tool-call id must NOT emit the key, so it stays
	// byte-compatible with a v19 reader.
	bare, err := json.Marshal(EventFrame{Seq: 8, Kind: EventKindPermissionRequest, RequestID: "p1"})
	if err != nil {
		t.Fatalf("Marshal bare permission.requested: %v", err)
	}
	if containsKey(bare, "toolCallId") {
		t.Fatalf("did not expect %q key on a frame without a tool-call id, got %s", "toolCallId", bare)
	}
}
