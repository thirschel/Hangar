package proto

import (
	"encoding/json"
	"testing"
)

func TestVersionV12(t *testing.T) {
	if Version != 12 {
		t.Fatalf("Version = %d, want 12", Version)
	}
}

func TestRespondMethodConstants(t *testing.T) {
	if MethodRespondPermission != "RespondPermission" {
		t.Fatalf("MethodRespondPermission = %q", MethodRespondPermission)
	}
	if MethodRespondUserInput != "RespondUserInput" {
		t.Fatalf("MethodRespondUserInput = %q", MethodRespondUserInput)
	}
}

func TestDecisionConstants(t *testing.T) {
	if DecisionApprove != "approve" || DecisionReject != "reject" {
		t.Fatalf("decision constants: approve=%q reject=%q", DecisionApprove, DecisionReject)
	}
}

func TestRespondPermissionRequestRoundTrip(t *testing.T) {
	req := Request{
		ID:        9,
		Method:    MethodRespondPermission,
		Session:   "ws-1",
		RequestID: "perm-abc",
		Decision:  DecisionApprove,
	}
	b, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var got Request
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if got.Method != MethodRespondPermission || got.Session != "ws-1" ||
		got.RequestID != "perm-abc" || got.Decision != DecisionApprove {
		t.Fatalf("RespondPermission round-trip mismatch: %+v", got)
	}
}

func TestRespondUserInputRequestRoundTrip(t *testing.T) {
	req := Request{
		ID:        10,
		Method:    MethodRespondUserInput,
		Session:   "ws-1",
		RequestID: "ui-xyz",
		Answer:    "Option B",
		Freeform:  true,
	}
	b, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var got Request
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if got.Method != MethodRespondUserInput || got.RequestID != "ui-xyz" ||
		got.Answer != "Option B" || !got.Freeform {
		t.Fatalf("RespondUserInput round-trip mismatch: %+v", got)
	}
}

// TestRespondFieldsOmittedWhenEmpty guards the additive contract: a non-respond
// request must not serialize the v12 fields, so older hosts/clients see no change.
func TestRespondFieldsOmittedWhenEmpty(t *testing.T) {
	b, err := json.Marshal(Request{ID: 1, Method: MethodSendMessage, Session: "ws", Message: "hi"})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	for _, field := range []string{"requestId", "decision", "answer", "freeform"} {
		if containsKey(b, field) {
			t.Fatalf("expected %q to be omitted, got %s", field, b)
		}
	}
}

func containsKey(b []byte, key string) bool {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(b, &m); err != nil {
		return false
	}
	_, ok := m[key]
	return ok
}
