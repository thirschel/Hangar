package proto

import (
	"encoding/json"
	"testing"
)

func TestVersionV18(t *testing.T) {
	if Version != 18 {
		t.Fatalf("Version = %d, want 18", Version)
	}
}

// TestV18EventKinds guards the three new rich-stream Kinds the desktop matches on
// to dismiss answered cards and restore the model selector after a restart.
func TestV18EventKinds(t *testing.T) {
	cases := map[string]string{
		EventKindPermissionResolved: "permission.resolved",
		EventKindInputResolved:      "input.resolved",
		EventKindModel:              "model",
	}
	for got, want := range cases {
		if got != want {
			t.Fatalf("event kind = %q, want %q", got, want)
		}
	}
}

// TestPermissionResolvedRoundTrip guards the v18 wire shape for a permission.resolved
// frame: it carries the answered request id plus the Decision ("approve"|"reject")
// under the "decision" JSON key the desktop reads to dismiss the permission card.
func TestPermissionResolvedRoundTrip(t *testing.T) {
	for _, decision := range []string{DecisionApprove, DecisionReject} {
		frame := EventFrame{Seq: 7, Kind: EventKindPermissionResolved, RequestID: "perm-1", Decision: decision}
		b, err := json.Marshal(frame)
		if err != nil {
			t.Fatalf("Marshal permission.resolved: %v", err)
		}
		var got EventFrame
		if err := json.Unmarshal(b, &got); err != nil {
			t.Fatalf("Unmarshal permission.resolved: %v", err)
		}
		if got.Kind != EventKindPermissionResolved || got.RequestID != "perm-1" || got.Decision != decision {
			t.Fatalf("permission.resolved round-trip = %+v, want decision=%q", got, decision)
		}
		if !containsKey(b, "decision") {
			t.Fatalf("expected %q key on a permission.resolved frame, got %s", "decision", b)
		}
	}
}

// TestInputResolvedRoundTrip guards the v18 wire shape for an input.resolved frame:
// it carries only the resolved user-input/elicitation request id so the desktop can
// dismiss the matching prompt UI.
func TestInputResolvedRoundTrip(t *testing.T) {
	frame := EventFrame{Seq: 8, Kind: EventKindInputResolved, RequestID: "ui-9"}
	b, err := json.Marshal(frame)
	if err != nil {
		t.Fatalf("Marshal input.resolved: %v", err)
	}
	var got EventFrame
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("Unmarshal input.resolved: %v", err)
	}
	if got.Kind != EventKindInputResolved || got.RequestID != "ui-9" {
		t.Fatalf("input.resolved round-trip = %+v", got)
	}
	if !containsKey(b, "requestId") {
		t.Fatalf("expected %q key on an input.resolved frame, got %s", "requestId", b)
	}
}

// TestModelFrameRoundTrip guards the v18 wire shape for a model frame: it carries the
// session's active Model plus the Effort and ContextTier under the "model"/"effort"/
// "contextTier" JSON keys the desktop reads to restore the model selector on resume.
func TestModelFrameRoundTrip(t *testing.T) {
	frame := EventFrame{Seq: 3, Kind: EventKindModel, Model: "gpt-5", Effort: "high", ContextTier: "long_context"}
	b, err := json.Marshal(frame)
	if err != nil {
		t.Fatalf("Marshal model: %v", err)
	}
	var got EventFrame
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("Unmarshal model: %v", err)
	}
	if got.Kind != EventKindModel || got.Model != "gpt-5" || got.Effort != "high" || got.ContextTier != "long_context" {
		t.Fatalf("model frame round-trip = %+v", got)
	}
	for _, key := range []string{"model", "effort", "contextTier"} {
		if !containsKey(b, key) {
			t.Fatalf("expected %q key on a model frame, got %s", key, b)
		}
	}
}

// TestV18FieldsOmittedWhenEmpty guards the additive contract: a frame without the
// v18 fields must NOT serialize decision/effort/contextTier, so every pre-v18 frame
// is byte-for-byte unchanged for older clients.
func TestV18FieldsOmittedWhenEmpty(t *testing.T) {
	b, err := json.Marshal(EventFrame{Seq: 1, Kind: EventKindIdle})
	if err != nil {
		t.Fatalf("Marshal frame: %v", err)
	}
	for _, key := range []string{"decision", "effort", "contextTier"} {
		if containsKey(b, key) {
			t.Fatalf("expected %q omitted on a frame without v18 fields, got %s", key, b)
		}
	}
}
