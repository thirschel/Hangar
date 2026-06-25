package proto

import (
	"encoding/json"
	"testing"
)

func TestVersionV16(t *testing.T) {
	// v16 (per-model reasoning effort + context tier) is a floor, not the current
	// version: once shipped, its surface must remain present in every later version.
	if Version < 16 {
		t.Fatalf("Version = %d, want >= 16", Version)
	}
}

// TestModelEffortAndContextTierRoundTrip guards the v16 wire shape: a SetModel
// request carries Request.Effort and Request.ContextTier alongside Request.Model,
// and a ListModels response advertises each model's reasoning-effort options via
// ModelInfo.SupportedEfforts/DefaultEffort. The fields serialize under the
// "effort"/"contextTier" and "supportedEfforts"/"defaultEffort" JSON keys the
// desktop renderer reads.
func TestModelEffortAndContextTierRoundTrip(t *testing.T) {
	req := Request{
		ID:          9,
		Method:      MethodSetModel,
		Session:     "ws-1",
		Model:       "gpt-5",
		Effort:      "high",
		ContextTier: "long_context",
	}
	b, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("Marshal request: %v", err)
	}
	var gotReq Request
	if err := json.Unmarshal(b, &gotReq); err != nil {
		t.Fatalf("Unmarshal request: %v", err)
	}
	if gotReq.Method != MethodSetModel || gotReq.Session != "ws-1" || gotReq.Model != "gpt-5" ||
		gotReq.Effort != "high" || gotReq.ContextTier != "long_context" {
		t.Fatalf("SetModel request round-trip = %+v", gotReq)
	}
	for _, key := range []string{"model", "effort", "contextTier"} {
		if !containsKey(b, key) {
			t.Fatalf("expected %q key on a SetModel request, got %s", key, b)
		}
	}

	resp := Response{ID: 9, OK: true, Models: []ModelInfo{
		{ID: "gpt-5", Name: "GPT-5", SupportedEfforts: []string{"low", "medium", "high", "xhigh"}, DefaultEffort: "high"},
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
	if len(gotResp.Models) != 2 {
		t.Fatalf("Models len = %d, want 2", len(gotResp.Models))
	}
	m0 := gotResp.Models[0]
	if m0.ID != "gpt-5" || m0.Name != "GPT-5" || m0.DefaultEffort != "high" ||
		len(m0.SupportedEfforts) != 4 || m0.SupportedEfforts[0] != "low" || m0.SupportedEfforts[3] != "xhigh" {
		t.Fatalf("model0 round-trip = %+v", m0)
	}
	// supportedEfforts/defaultEffort are keys ON a ModelInfo (they sit nested inside
	// Response.models, so containsKey on the whole response would never see them);
	// assert the JSON shape on a marshaled model directly.
	mb, err := json.Marshal(ModelInfo{ID: "gpt-5", Name: "GPT-5", SupportedEfforts: []string{"low", "high"}, DefaultEffort: "high"})
	if err != nil {
		t.Fatalf("Marshal model: %v", err)
	}
	for _, key := range []string{"supportedEfforts", "defaultEffort"} {
		if !containsKey(mb, key) {
			t.Fatalf("expected %q key on a ModelInfo, got %s", key, mb)
		}
	}
}

// TestEffortTierAndModelEffortsOmittedWhenEmpty guards the additive contract: a
// SetModel request without effort/tier and a ModelInfo without reasoning-effort
// options must NOT serialize the v16 fields, so a v14/v15 model switch is
// byte-for-byte unchanged for older clients.
func TestEffortTierAndModelEffortsOmittedWhenEmpty(t *testing.T) {
	reqB, err := json.Marshal(Request{ID: 1, Method: MethodSetModel, Session: "ws", Model: "gpt-5"})
	if err != nil {
		t.Fatalf("Marshal request: %v", err)
	}
	for _, key := range []string{"effort", "contextTier"} {
		if containsKey(reqB, key) {
			t.Fatalf("expected %q omitted on a SetModel without effort/tier, got %s", key, reqB)
		}
	}

	// supportedEfforts/defaultEffort live ON a ModelInfo; assert they're omitted on a
	// model with no reasoning-effort options (containsKey reads top-level keys, so it
	// must run against the marshaled model, not the enclosing response).
	mb, err := json.Marshal(ModelInfo{ID: "gpt-5", Name: "GPT-5"})
	if err != nil {
		t.Fatalf("Marshal model: %v", err)
	}
	for _, key := range []string{"supportedEfforts", "defaultEffort"} {
		if containsKey(mb, key) {
			t.Fatalf("expected %q omitted on a model without efforts, got %s", key, mb)
		}
	}
}
