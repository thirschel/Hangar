package proto

import (
	"encoding/json"
	"testing"
)

func TestVersionV23(t *testing.T) {
	if Version < 23 {
		t.Fatalf("Version = %d, want >= 23", Version)
	}
}

// TestV23InstructionsRoundTrip guards the v23 wire addition: EventKindInstructions
// carries the full InstructionInfo list (the custom instructions the SDK loaded for
// the session) under the "instructions" JSON key, replacing the desktop's
// Instructions page wholesale. omitempty keeps a frame without instructions
// byte-compatible with a v22 reader.
func TestV23InstructionsRoundTrip(t *testing.T) {
	if EventKindInstructions != "instructions" {
		t.Fatalf("EventKindInstructions = %q, want instructions", EventKindInstructions)
	}

	frame := EventFrame{
		Seq:  7,
		Kind: EventKindInstructions,
		Instructions: []InstructionInfo{
			{
				Label:       "Repo instructions",
				SourcePath:  ".github/copilot-instructions.md",
				Type:        "repository",
				Location:    "repository",
				Description: "House style",
				ApplyTo:     []string{"**/*.go"},
				Content:     "Always run gofmt.",
			},
			{Label: "Home", SourcePath: "~/.copilot/instructions.md"},
		},
	}
	b, err := json.Marshal(frame)
	if err != nil {
		t.Fatalf("Marshal instructions: %v", err)
	}
	if !containsKey(b, "instructions") {
		t.Fatalf("expected %q key, got %s", "instructions", b)
	}

	var got EventFrame
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("Unmarshal instructions: %v", err)
	}
	if got.Kind != EventKindInstructions || len(got.Instructions) != 2 {
		t.Fatalf("instructions frame round-trip = %+v", got)
	}
	first := got.Instructions[0]
	if first.Label != "Repo instructions" || first.SourcePath != ".github/copilot-instructions.md" ||
		first.Type != "repository" || first.Location != "repository" || first.Description != "House style" ||
		len(first.ApplyTo) != 1 || first.ApplyTo[0] != "**/*.go" || first.Content != "Always run gofmt." {
		t.Fatalf("instruction0 = %+v", first)
	}
	second := got.Instructions[1]
	if second.Label != "Home" || second.SourcePath != "~/.copilot/instructions.md" ||
		second.Type != "" || second.Description != "" || second.Content != "" || len(second.ApplyTo) != 0 {
		t.Fatalf("instruction1 = %+v", second)
	}

	// omitempty: a frame without instructions must NOT emit the key, so it stays
	// byte-compatible with a v22 reader.
	bare, err := json.Marshal(EventFrame{Seq: 8, Kind: EventKindIdle})
	if err != nil {
		t.Fatalf("Marshal bare: %v", err)
	}
	if containsKey(bare, "instructions") {
		t.Fatalf("did not expect %q key on a bare frame, got %s", "instructions", bare)
	}
}
