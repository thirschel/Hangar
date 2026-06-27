//go:build windows

package winhost

import (
	"context"
	"testing"
	"time"

	copilot "github.com/github/copilot-sdk/go"
	"hangar/session/copilotsdk"
	"hangar/session/winhost/proto"
)

// TestSendMessageThreadsAgentMode proves a v25 MethodSendMessage request's AgentMode
// ("plan"|"autopilot"|"") reaches sdkSession.richSend and onward to the SDK. The send
// runs on a goroutine, so we intercept the SDK boundary with a capturing sendFn.
func TestSendMessageThreadsAgentMode(t *testing.T) {
	_, h, cleanup := startTestHostWithHandle(t)
	defer cleanup()

	rich := newSDKSession(sdkSessionParams{name: "rich-mode", program: "copilot", workDir: t.TempDir()}, nil, nil)
	defer rich.close()

	got := make(chan string, 1)
	rich.sendFn = func(_ context.Context, _ string, _ []string, mode string) error {
		got <- mode
		return nil
	}

	h.mu.Lock()
	h.sessions["rich-mode"] = rich
	h.mu.Unlock()

	resp := h.dispatch(&proto.Request{ID: 1, Method: proto.MethodSendMessage, Session: "rich-mode", Message: "go", AgentMode: "plan"})
	if !resp.OK {
		t.Fatalf("SendMessage dispatch = %+v, want OK", resp)
	}
	select {
	case mode := <-got:
		if mode != "plan" {
			t.Fatalf("richSend mode = %q, want %q", mode, "plan")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("richSend was not called within 2s")
	}
}

// TestOnSDKPlanEmitsRequestFrame proves a plan prompt surfaces as an
// exit_plan_mode.requested frame on the rich stream (the plan-review card source).
func TestOnSDKPlanEmitsRequestFrame(t *testing.T) {
	s := newSDKSession(sdkSessionParams{name: "rich-plan", program: "copilot", workDir: t.TempDir()}, nil, nil)
	defer s.close()

	s.onSDKPlan(copilotsdk.PlanPrompt{
		RequestID:         "plan-1",
		Summary:           "Add modes",
		PlanContent:       "## plan",
		Actions:           []string{"interactive", "autopilot", "exit_only"},
		RecommendedAction: "autopilot",
	})

	frames := s.richTranscript(0)
	if len(frames) != 1 {
		t.Fatalf("richTranscript returned %d frames, want 1", len(frames))
	}
	f := frames[0]
	if f.Kind != proto.EventKindExitPlanModeRequest || f.RequestID != "plan-1" || f.Summary != "Add modes" ||
		f.PlanContent != "## plan" || f.RecommendedAction != "autopilot" || len(f.Actions) != 3 || f.Actions[1] != "autopilot" {
		t.Fatalf("plan frame = %+v", f)
	}
}

// TestRichRespondExitPlanModeNoPending proves answering an unknown plan request is a
// reported no-op rather than a panic.
func TestRichRespondExitPlanModeNoPending(t *testing.T) {
	s := newSDKSession(sdkSessionParams{name: "rich-plan2", program: "copilot", workDir: t.TempDir()}, nil, nil)
	defer s.close()
	if err := s.richRespondExitPlanMode("nope", true, "autopilot", ""); err == nil {
		t.Fatal("richRespondExitPlanMode on an unknown id should error")
	}
}

// TestExitPlanModeCompletedFrame proves the SDK completion event becomes an
// exit_plan_mode.resolved frame carrying the approval + chosen action, so a resumed
// transcript dismisses the plan-review card instead of re-showing it.
func TestExitPlanModeCompletedFrame(t *testing.T) {
	approved := true
	action := copilot.ExitPlanModeActionAutopilot
	frame, ok := sdkEventFrame(copilot.SessionEvent{Data: &copilot.ExitPlanModeCompletedData{
		RequestID:      "plan-1",
		Approved:       &approved,
		SelectedAction: &action,
	}})
	if !ok {
		t.Fatal("sdkEventFrame did not translate ExitPlanModeCompletedData")
	}
	if frame.Kind != proto.EventKindExitPlanModeResolved || frame.RequestID != "plan-1" || !frame.Approved || frame.SelectedAction != "autopilot" {
		t.Fatalf("resolved frame = %+v", frame)
	}
}
