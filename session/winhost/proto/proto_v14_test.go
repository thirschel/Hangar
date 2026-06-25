package proto

import (
	"encoding/json"
	"testing"
)

func TestVersionV14(t *testing.T) {
	if Version != 14 {
		t.Fatalf("Version = %d, want 14", Version)
	}
}

func TestUsageEventKindAndModelMethods(t *testing.T) {
	if EventKindUsage != "usage" {
		t.Fatalf("EventKindUsage = %q, want usage", EventKindUsage)
	}
	if MethodListModels != "ListModels" {
		t.Fatalf("MethodListModels = %q, want ListModels", MethodListModels)
	}
	if MethodSetModel != "SetModel" {
		t.Fatalf("MethodSetModel = %q, want SetModel", MethodSetModel)
	}
}

// TestUsageFrameRoundTrip guards the v14 wire shape: a usage frame carries the
// active model plus the context-window token counts the desktop renders as a
// context percentage (CurrentTokens/TokenLimit).
func TestUsageFrameRoundTrip(t *testing.T) {
	frame := EventFrame{
		Seq:           5,
		Kind:          EventKindUsage,
		Model:         "claude-sonnet-4.5",
		CurrentTokens: 12000,
		TokenLimit:    200000,
	}
	b, err := json.Marshal(frame)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var got EventFrame
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if got.Kind != EventKindUsage || got.Model != "claude-sonnet-4.5" ||
		got.CurrentTokens != 12000 || got.TokenLimit != 200000 {
		t.Fatalf("usage round-trip = %+v", got)
	}
	for _, key := range []string{"model", "currentTokens", "tokenLimit"} {
		if !containsKey(b, key) {
			t.Fatalf("expected %q key on usage frame, got %s", key, b)
		}
	}
}

// TestModelSelectorRoundTrip guards the v14 request/response surface: SetModel
// carries Request.Model and ListModels returns Response.Models.
func TestModelSelectorRoundTrip(t *testing.T) {
	req := Request{ID: 1, Method: MethodSetModel, Session: "ws-1", Model: "gpt-5"}
	b, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("Marshal request: %v", err)
	}
	var gotReq Request
	if err := json.Unmarshal(b, &gotReq); err != nil {
		t.Fatalf("Unmarshal request: %v", err)
	}
	if gotReq.Method != MethodSetModel || gotReq.Session != "ws-1" || gotReq.Model != "gpt-5" {
		t.Fatalf("SetModel request round-trip = %+v", gotReq)
	}

	resp := Response{ID: 1, OK: true, Models: []ModelInfo{
		{ID: "gpt-5", Name: "GPT-5"},
		{ID: "claude-sonnet-4.5"},
	}}
	rb, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("Marshal response: %v", err)
	}
	var gotResp Response
	if err := json.Unmarshal(rb, &gotResp); err != nil {
		t.Fatalf("Unmarshal response: %v", err)
	}
	if len(gotResp.Models) != 2 ||
		gotResp.Models[0] != (ModelInfo{ID: "gpt-5", Name: "GPT-5"}) ||
		gotResp.Models[1] != (ModelInfo{ID: "claude-sonnet-4.5"}) {
		t.Fatalf("ListModels response round-trip = %+v", gotResp.Models)
	}
	if !containsKey(rb, "models") {
		t.Fatalf("expected models key, got %s", rb)
	}
}

// TestUsageAndModelFieldsOmittedWhenEmpty guards the additive contract: a non-usage
// frame, a non-SetModel request, and a non-ListModels response must not serialize
// the v14 fields, so older code paths are unchanged.
func TestUsageAndModelFieldsOmittedWhenEmpty(t *testing.T) {
	fb, err := json.Marshal(EventFrame{Seq: 1, Kind: EventKindIdle})
	if err != nil {
		t.Fatalf("Marshal frame: %v", err)
	}
	for _, key := range []string{"model", "currentTokens", "tokenLimit"} {
		if containsKey(fb, key) {
			t.Fatalf("expected %q omitted on a non-usage frame, got %s", key, fb)
		}
	}
	reqB, err := json.Marshal(Request{ID: 1, Method: MethodSendMessage, Session: "ws", Message: "hi"})
	if err != nil {
		t.Fatalf("Marshal request: %v", err)
	}
	if containsKey(reqB, "model") {
		t.Fatalf("expected model omitted on a non-SetModel request, got %s", reqB)
	}
	respB, err := json.Marshal(Response{ID: 1, OK: true})
	if err != nil {
		t.Fatalf("Marshal response: %v", err)
	}
	if containsKey(respB, "models") {
		t.Fatalf("expected models omitted on a non-ListModels response, got %s", respB)
	}
}
