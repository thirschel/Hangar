package proto

import (
	"encoding/json"
	"testing"
)

func TestVersionV25(t *testing.T) {
	// v25 (Plan/Autopilot work modes) is a floor, not the current version: once
	// shipped, its surface must remain present in every later version.
	if Version < 25 {
		t.Fatalf("Version = %d, want >= 25", Version)
	}
}

// TestV25MethodAndEventKinds pins the wire names the desktop and host agree on for
// plan/autopilot mode: the respond method and the two exit-plan-mode event kinds.
func TestV25MethodAndEventKinds(t *testing.T) {
	cases := map[string]string{
		MethodRespondExitPlanMode:     "RespondExitPlanMode",
		EventKindExitPlanModeRequest:  "exit_plan_mode.requested",
		EventKindExitPlanModeResolved: "exit_plan_mode.resolved",
	}
	for got, want := range cases {
		if got != want {
			t.Fatalf("name = %q, want %q", got, want)
		}
	}
}

// TestExitPlanModeRequestedRoundTrip guards the v25 wire shape for the plan frame:
// it carries the SDK ExitPlanModeRequest (summary + plan content + actions +
// recommended action) the desktop renders as a plan-review card.
func TestExitPlanModeRequestedRoundTrip(t *testing.T) {
	frame := EventFrame{
		Seq:               11,
		Kind:              EventKindExitPlanModeRequest,
		RequestID:         "plan-1",
		Summary:           "Add plan/autopilot modes",
		PlanContent:       "## Steps\n1. proto\n2. daemon",
		Actions:           []string{"interactive", "autopilot", "exit_only"},
		RecommendedAction: "autopilot",
	}
	b, err := json.Marshal(frame)
	if err != nil {
		t.Fatalf("Marshal exit_plan_mode.requested: %v", err)
	}
	var got EventFrame
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("Unmarshal exit_plan_mode.requested: %v", err)
	}
	if got.Kind != EventKindExitPlanModeRequest || got.RequestID != "plan-1" ||
		got.Summary != "Add plan/autopilot modes" || got.PlanContent != "## Steps\n1. proto\n2. daemon" ||
		got.RecommendedAction != "autopilot" || len(got.Actions) != 3 || got.Actions[1] != "autopilot" {
		t.Fatalf("exit_plan_mode.requested round-trip = %+v", got)
	}
	for _, key := range []string{"summary", "planContent", "actions", "recommendedAction"} {
		if !containsKey(b, key) {
			t.Fatalf("expected %q key on an exit_plan_mode.requested frame, got %s", key, b)
		}
	}
}

// TestExitPlanModeResolvedRoundTrip guards the v25 wire shape for the resume-replay
// frame: it carries the answered request id plus the approval + chosen action so the
// desktop can dismiss the plan card on resume.
func TestExitPlanModeResolvedRoundTrip(t *testing.T) {
	frame := EventFrame{Seq: 12, Kind: EventKindExitPlanModeResolved, RequestID: "plan-1", Approved: true, SelectedAction: "autopilot"}
	b, err := json.Marshal(frame)
	if err != nil {
		t.Fatalf("Marshal exit_plan_mode.resolved: %v", err)
	}
	var got EventFrame
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("Unmarshal exit_plan_mode.resolved: %v", err)
	}
	if got.Kind != EventKindExitPlanModeResolved || got.RequestID != "plan-1" || !got.Approved || got.SelectedAction != "autopilot" {
		t.Fatalf("exit_plan_mode.resolved round-trip = %+v", got)
	}
	for _, key := range []string{"requestId", "approved", "selectedAction"} {
		if !containsKey(b, key) {
			t.Fatalf("expected %q key on an exit_plan_mode.resolved frame, got %s", key, b)
		}
	}
}

// TestSendMessageAgentModeRoundTrip guards the v25 send shape: a SendMessage request
// carries the per-turn AgentMode ("plan"|"autopilot"|"") under the "agentMode" key.
func TestSendMessageAgentModeRoundTrip(t *testing.T) {
	req := Request{ID: 1, Method: MethodSendMessage, Session: "ws", Message: "go", AgentMode: "plan"}
	b, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("Marshal SendMessage: %v", err)
	}
	var got Request
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("Unmarshal SendMessage: %v", err)
	}
	if got.Method != MethodSendMessage || got.Message != "go" || got.AgentMode != "plan" {
		t.Fatalf("SendMessage AgentMode round-trip = %+v", got)
	}
	if !containsKey(b, "agentMode") {
		t.Fatalf("expected %q key on a SendMessage request, got %s", "agentMode", b)
	}
}

// TestRespondExitPlanModeRoundTrip guards the v25 answer shape: a RespondExitPlanMode
// request carries the request id plus Approved/SelectedAction/Feedback.
func TestRespondExitPlanModeRoundTrip(t *testing.T) {
	req := Request{ID: 2, Method: MethodRespondExitPlanMode, Session: "ws", RequestID: "plan-1", Approved: true, SelectedAction: "interactive", Feedback: "looks good"}
	b, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("Marshal RespondExitPlanMode: %v", err)
	}
	var got Request
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("Unmarshal RespondExitPlanMode: %v", err)
	}
	if got.Method != MethodRespondExitPlanMode || got.RequestID != "plan-1" || !got.Approved ||
		got.SelectedAction != "interactive" || got.Feedback != "looks good" {
		t.Fatalf("RespondExitPlanMode round-trip = %+v", got)
	}
	for _, key := range []string{"requestId", "approved", "selectedAction", "feedback"} {
		if !containsKey(b, key) {
			t.Fatalf("expected %q key on a RespondExitPlanMode request, got %s", key, b)
		}
	}
}

// TestV25FieldsOmittedWhenEmpty guards the additive contract: a frame/request without
// the v25 fields must NOT serialize them, so every pre-v25 message is byte-for-byte
// unchanged for older clients.
func TestV25FieldsOmittedWhenEmpty(t *testing.T) {
	fb, err := json.Marshal(EventFrame{Seq: 1, Kind: EventKindIdle})
	if err != nil {
		t.Fatalf("Marshal frame: %v", err)
	}
	for _, key := range []string{"summary", "planContent", "actions", "recommendedAction", "selectedAction", "approved"} {
		if containsKey(fb, key) {
			t.Fatalf("expected %q omitted on a frame without v25 fields, got %s", key, fb)
		}
	}
	rb, err := json.Marshal(Request{ID: 1, Method: MethodSendMessage, Session: "ws", Message: "hi"})
	if err != nil {
		t.Fatalf("Marshal request: %v", err)
	}
	for _, key := range []string{"agentMode", "approved", "selectedAction", "feedback"} {
		if containsKey(rb, key) {
			t.Fatalf("expected %q omitted on a request without v25 fields, got %s", key, rb)
		}
	}
}
