package proto

import (
	"encoding/json"
	"testing"
)

func TestVersionV17(t *testing.T) {
	if Version != 17 {
		t.Fatalf("Version = %d, want 17", Version)
	}
}

// TestToolArgsAndResultRoundTrip guards the v17 wire shape: a tool.start frame
// carries a concise EventFrame.ToolArgs and a tool.complete frame a concise
// EventFrame.ToolResult, serialized under the "toolArgs"/"toolResult" JSON keys the
// desktop renderer reads to draw CLI-style tool lines (name + args + result).
func TestToolArgsAndResultRoundTrip(t *testing.T) {
	start := EventFrame{Seq: 1, Kind: EventKindToolStart, ToolName: "read_file", ToolArgs: "README.md"}
	sb, err := json.Marshal(start)
	if err != nil {
		t.Fatalf("Marshal tool.start: %v", err)
	}
	var gotStart EventFrame
	if err := json.Unmarshal(sb, &gotStart); err != nil {
		t.Fatalf("Unmarshal tool.start: %v", err)
	}
	if gotStart.Kind != EventKindToolStart || gotStart.ToolName != "read_file" || gotStart.ToolArgs != "README.md" {
		t.Fatalf("tool.start round-trip = %+v", gotStart)
	}
	if !containsKey(sb, "toolArgs") {
		t.Fatalf("expected %q key on a tool.start frame, got %s", "toolArgs", sb)
	}

	complete := EventFrame{Seq: 2, Kind: EventKindToolComplete, ToolName: "read_file", ToolResult: "150 lines read"}
	cb, err := json.Marshal(complete)
	if err != nil {
		t.Fatalf("Marshal tool.complete: %v", err)
	}
	var gotComplete EventFrame
	if err := json.Unmarshal(cb, &gotComplete); err != nil {
		t.Fatalf("Unmarshal tool.complete: %v", err)
	}
	if gotComplete.Kind != EventKindToolComplete || gotComplete.ToolName != "read_file" || gotComplete.ToolResult != "150 lines read" {
		t.Fatalf("tool.complete round-trip = %+v", gotComplete)
	}
	if !containsKey(cb, "toolResult") {
		t.Fatalf("expected %q key on a tool.complete frame, got %s", "toolResult", cb)
	}
}

// TestToolArgsAndResultOmittedWhenEmpty guards the additive contract: a tool frame
// without a summary must NOT serialize the v17 fields, so a v16 tool.start/
// tool.complete frame is byte-for-byte unchanged for older clients.
func TestToolArgsAndResultOmittedWhenEmpty(t *testing.T) {
	b, err := json.Marshal(EventFrame{Seq: 1, Kind: EventKindToolStart, ToolName: "read_file"})
	if err != nil {
		t.Fatalf("Marshal frame: %v", err)
	}
	for _, key := range []string{"toolArgs", "toolResult"} {
		if containsKey(b, key) {
			t.Fatalf("expected %q omitted on a tool frame without summaries, got %s", key, b)
		}
	}
}
