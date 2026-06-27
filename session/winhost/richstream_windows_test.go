//go:build windows

package winhost

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"
	"unsafe"

	copilot "github.com/github/copilot-sdk/go"
	"hangar/session/copilotsdk"
	"hangar/session/winhost/proto"
)

func TestSDKSessionRichEventsAndReplay(t *testing.T) {
	s := newSDKSession(sdkSessionParams{name: "rich1", program: "copilot", workDir: t.TempDir()}, nil, nil)
	defer s.close()

	mcpServer := "github"
	toolCallID := "tc1"
	aborted := true
	events := []copilot.SessionEvent{
		{Data: &copilot.AssistantMessageData{Content: "hello", MessageID: "m1"}},
		{Data: &copilot.AssistantMessageDeltaData{DeltaContent: " world", MessageID: "m1"}},
		{Data: &copilot.AssistantReasoningData{Content: "thinking", ReasoningID: "r1"}},
		{Data: &copilot.ToolExecutionStartData{ToolName: "read_file", ToolCallID: "tc1", MCPServerName: &mcpServer}},
		{Data: &copilot.ToolExecutionCompleteData{
			ToolCallID: "tc1",
			Success:    true,
			ToolDescription: &copilot.ToolExecutionCompleteToolDescription{
				Name: "read_file",
			},
		}},
		{Data: &copilot.PermissionRequestedData{
			RequestID: "perm1",
			PermissionRequest: &copilot.PermissionRequestShell{
				ToolCallID: &toolCallID,
			},
		}},
		{Data: &copilot.SessionTitleChangedData{Title: "New title"}},
		{Data: &copilot.SessionIdleData{Aborted: &aborted}},
	}

	for _, ev := range events {
		s.onSDKEvent(ev)
	}

	frames := s.richTranscript(0)
	if len(frames) != len(events) {
		t.Fatalf("richTranscript returned %d frames, want %d", len(frames), len(events))
	}
	for i, frame := range frames {
		wantSeq := uint64(i + 1)
		if frame.Seq != wantSeq {
			t.Fatalf("frame %d Seq = %d, want %d", i, frame.Seq, wantSeq)
		}
	}

	assertFrame := func(idx int, kind, text string) {
		t.Helper()
		if frames[idx].Kind != kind || frames[idx].Text != text {
			t.Fatalf("frame %d = %+v, want kind=%q text=%q", idx, frames[idx], kind, text)
		}
	}
	assertFrame(0, proto.EventKindAssistantMessage, "hello")
	assertFrame(1, proto.EventKindAssistantDelta, " world")
	assertFrame(2, proto.EventKindReasoning, "thinking")
	if frames[3].Kind != proto.EventKindToolStart || frames[3].ToolCallID != "tc1" || frames[3].ToolName != "read_file" || frames[3].MCPServer != "github" {
		t.Fatalf("tool start frame = %+v", frames[3])
	}
	if frames[4].Kind != proto.EventKindToolComplete || frames[4].ToolCallID != "tc1" || frames[4].ToolName != "read_file" {
		t.Fatalf("tool complete frame = %+v", frames[4])
	}
	if frames[5].Kind != proto.EventKindPermissionRequest || frames[5].RequestID != "perm1" || frames[5].ToolCallID != "tc1" {
		t.Fatalf("permission frame = %+v", frames[5])
	}
	if frames[6].Kind != proto.EventKindTitle || frames[6].Title != "New title" {
		t.Fatalf("title frame = %+v", frames[6])
	}
	if frames[7].Kind != proto.EventKindIdle || !frames[7].Aborted {
		t.Fatalf("idle frame = %+v", frames[7])
	}

	snapshot, sub := s.richSubscribe(3)
	defer s.richUnsubscribe(sub)
	if len(snapshot) != len(events)-3 {
		t.Fatalf("richSubscribe snapshot returned %d frames, want %d", len(snapshot), len(events)-3)
	}
	if snapshot[0].Seq != 4 || snapshot[len(snapshot)-1].Seq != uint64(len(events)) {
		t.Fatalf("snapshot Seq range = %d..%d", snapshot[0].Seq, snapshot[len(snapshot)-1].Seq)
	}

	s.onSDKEvent(copilot.SessionEvent{Data: &copilot.AssistantMessageData{Content: "live", MessageID: "m2"}})
	select {
	case raw := <-sub.ch:
		var live proto.EventFrame
		if err := json.Unmarshal(raw, &live); err != nil {
			t.Fatalf("live frame unmarshal: %v", err)
		}
		if live.Seq != uint64(len(events)+1) || live.Kind != proto.EventKindAssistantMessage || live.Text != "live" {
			t.Fatalf("live frame = %+v", live)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for live rich frame")
	}
}

func TestSDKEventFrameIdleTimestamp(t *testing.T) {
	fixedTime := time.Date(2026, time.June, 25, 20, 18, 49, 563000000, time.UTC)
	frame, ok := sdkEventFrame(copilot.SessionEvent{
		Timestamp: fixedTime,
		Data:      &copilot.SessionIdleData{},
	})
	if !ok {
		t.Fatal("sdkEventFrame did not map SessionIdleData")
	}
	if frame.Kind != proto.EventKindIdle {
		t.Fatalf("idle frame Kind = %q, want %q", frame.Kind, proto.EventKindIdle)
	}
	if frame.Timestamp != fixedTime.UnixMilli() {
		t.Fatalf("idle frame Timestamp = %d, want %d", frame.Timestamp, fixedTime.UnixMilli())
	}

	zero, ok := sdkEventFrame(copilot.SessionEvent{Data: &copilot.SessionIdleData{}})
	if !ok {
		t.Fatal("sdkEventFrame did not map zero-timestamp SessionIdleData")
	}
	if zero.Kind != proto.EventKindIdle {
		t.Fatalf("zero-timestamp idle frame Kind = %q, want %q", zero.Kind, proto.EventKindIdle)
	}
	if zero.Timestamp != 0 {
		t.Fatalf("zero-timestamp idle frame Timestamp = %d, want 0", zero.Timestamp)
	}
}

func TestOnSDKEventAssistantUsageEmitsCachedUsageFrame(t *testing.T) {
	s := newSDKSession(sdkSessionParams{name: "rich-aic", program: "copilot", workDir: t.TempDir(), model: "gpt-5"}, nil, nil)
	defer s.close()
	seedSDKUsageForTest(t, s.sess, 9000, 200000, 3e9)

	s.onSDKEvent(copilot.SessionEvent{Data: &copilot.AssistantUsageData{
		CopilotUsage: &copilot.AssistantUsageCopilotUsage{TotalNanoAiu: 1e9},
	}})

	frames := s.richTranscript(0)
	if len(frames) != 1 {
		t.Fatalf("richTranscript returned %d frames, want 1", len(frames))
	}
	if frames[0].Kind != proto.EventKindUsage || frames[0].Model != "gpt-5" ||
		frames[0].CurrentTokens != 9000 || frames[0].TokenLimit != 200000 || frames[0].Aic != 3 {
		t.Fatalf("assistant usage frame = %+v", frames[0])
	}
}

func TestOnSDKEventAssistantUsageSkipsUnknownUsage(t *testing.T) {
	s := newSDKSession(sdkSessionParams{name: "rich-aic-unknown", program: "copilot", workDir: t.TempDir()}, nil, nil)
	defer s.close()
	seedSDKUsageForTest(t, s.sess, 0, 0, 1e9)

	s.onSDKEvent(copilot.SessionEvent{Data: &copilot.AssistantUsageData{
		CopilotUsage: &copilot.AssistantUsageCopilotUsage{TotalNanoAiu: 1e9},
	}})

	if frames := s.richTranscript(0); len(frames) != 0 {
		t.Fatalf("assistant usage with unknown tokens should emit no frame, got %+v", frames)
	}
}

func seedSDKUsageForTest(t *testing.T, sess *copilotsdk.Session, currentTokens, tokenLimit int64, aicNano float64) {
	t.Helper()
	v := reflect.ValueOf(sess).Elem()
	setInt64FieldForTest(t, v, "usageCurrent", currentTokens)
	setInt64FieldForTest(t, v, "usageLimit", tokenLimit)
	setFloat64FieldForTest(t, v, "usageAicNano", aicNano)
	if currentTokens != 0 || tokenLimit != 0 {
		setBoolFieldForTest(t, v, "usageKnown", true)
	}
}

func setInt64FieldForTest(t *testing.T, v reflect.Value, name string, value int64) {
	t.Helper()
	f := v.FieldByName(name)
	reflect.NewAt(f.Type(), unsafe.Pointer(f.UnsafeAddr())).Elem().SetInt(value)
}

func setFloat64FieldForTest(t *testing.T, v reflect.Value, name string, value float64) {
	t.Helper()
	f := v.FieldByName(name)
	reflect.NewAt(f.Type(), unsafe.Pointer(f.UnsafeAddr())).Elem().SetFloat(value)
}

func setBoolFieldForTest(t *testing.T, v reflect.Value, name string, value bool) {
	t.Helper()
	f := v.FieldByName(name)
	reflect.NewAt(f.Type(), unsafe.Pointer(f.UnsafeAddr())).Elem().SetBool(value)
}

func TestOnSDKEventMCPStatusFrames(t *testing.T) {
	s := newSDKSession(sdkSessionParams{name: "rich-mcp", program: "copilot", workDir: t.TempDir()}, nil, nil)
	defer s.close()

	failed := "boom"
	s.onSDKEvent(copilot.SessionEvent{Data: &copilot.SessionMCPServersLoadedData{Servers: []copilot.MCPServersLoadedServer{
		{Name: "github", Status: copilot.MCPServerStatusConnected},
		{Name: "broken", Status: copilot.MCPServerStatusFailed, Error: &failed},
	}}})
	s.onSDKEvent(copilot.SessionEvent{Data: &copilot.SessionMCPServerStatusChangedData{ServerName: "github", Status: copilot.MCPServerStatusNeedsAuth}})

	frames := s.richTranscript(0)
	if len(frames) != 3 {
		t.Fatalf("richTranscript returned %d frames, want 3", len(frames))
	}
	for _, f := range frames {
		if f.Kind != proto.EventKindMCPStatus {
			t.Fatalf("frame kind = %q, want %q", f.Kind, proto.EventKindMCPStatus)
		}
	}
	if frames[0].MCPServer != "github" || frames[0].Status != string(copilot.MCPServerStatusConnected) {
		t.Fatalf("loaded frame 0 = %+v", frames[0])
	}
	if frames[1].MCPServer != "broken" || frames[1].Status != string(copilot.MCPServerStatusFailed) || frames[1].Error != "boom" {
		t.Fatalf("loaded frame 1 = %+v", frames[1])
	}
	if frames[2].MCPServer != "github" || frames[2].Status != string(copilot.MCPServerStatusNeedsAuth) {
		t.Fatalf("status-changed frame = %+v", frames[2])
	}
}

func TestRefreshMCPStatusPollsPastEmptyAndTransientError(t *testing.T) {
	s := newSDKSession(sdkSessionParams{name: "rich-mcp-refresh", program: "copilot", workDir: t.TempDir()}, nil, nil)
	defer s.close()

	calls := 0
	s.mcpStatusFn = func(context.Context) ([]copilotsdk.MCPServerDetail, error) {
		calls++
		switch calls {
		case 1:
			return nil, nil
		case 2:
			return nil, errors.New("not listed yet")
		default:
			return []copilotsdk.MCPServerDetail{{Name: "github", Status: "connected", Source: "user"}}, nil
		}
	}

	if settled := s.refreshMCPStatus(context.Background()); settled {
		t.Fatal("empty MCP status should keep polling")
	}
	if settled := s.refreshMCPStatus(context.Background()); settled {
		t.Fatal("transient MCP status error should keep polling")
	}
	if frames := s.richTranscript(0); len(frames) != 0 {
		t.Fatalf("transient MCP status results should not emit frames, got %+v", frames)
	}
	if settled := s.refreshMCPStatus(context.Background()); !settled {
		t.Fatal("non-empty connected MCP status should settle")
	}

	frames := s.richTranscript(0)
	if len(frames) != 2 {
		t.Fatalf("richTranscript returned %d frames, want status + detail: %+v", len(frames), frames)
	}
	if frames[0].Kind != proto.EventKindMCPStatus || frames[0].MCPServer != "github" || frames[0].Status != "connected" {
		t.Fatalf("MCP status frame = %+v", frames[0])
	}
	if frames[1].Kind != proto.EventKindMCPDetail || len(frames[1].MCPServers) != 1 ||
		frames[1].MCPServers[0].Name != "github" || frames[1].MCPServers[0].Status != "connected" {
		t.Fatalf("MCP detail frame = %+v", frames[1])
	}
	if calls != 3 {
		t.Fatalf("MCP status calls = %d, want 3", calls)
	}
}

func TestPermissionFrameIncludesQuestionAndTool(t *testing.T) {
	toolCallID := "call-123"
	frame, ok := sdkEventFrame(copilot.SessionEvent{Data: &copilot.PermissionRequestedData{
		RequestID: "perm-shell",
		PromptRequest: &copilot.PermissionPromptRequestCommands{
			FullCommandText: "echo PERMTEST_1773",
			Intention:       "print the test marker",
			ToolCallID:      &toolCallID,
		},
	}})
	if !ok {
		t.Fatal("sdkEventFrame did not map permission request")
	}
	if frame.Kind != proto.EventKindPermissionRequest || frame.RequestID != "perm-shell" {
		t.Fatalf("permission frame basics = %+v", frame)
	}
	if frame.Question != "Run shell command: echo PERMTEST_1773" {
		t.Fatalf("permission Question = %q", frame.Question)
	}
	if frame.ToolName != "shell" {
		t.Fatalf("permission ToolName = %q, want shell", frame.ToolName)
	}
	if frame.ToolCallID != toolCallID {
		t.Fatalf("permission ToolCallID = %q, want %q", frame.ToolCallID, toolCallID)
	}
}

func TestSDKPromptEmitsUserInputFrame(t *testing.T) {
	s := newSDKSession(sdkSessionParams{name: "rich1", program: "copilot", workDir: t.TempDir()}, nil, nil)
	defer s.close()

	_, sub := s.richSubscribe(0)
	defer s.richUnsubscribe(sub)
	s.onSDKPrompt(copilotsdk.Prompt{
		Kind:      "user_input",
		RequestID: "ui-1",
		Question:  "Pick one",
		Choices:   []string{"A", "B"},
	})

	frames := s.richTranscript(0)
	if len(frames) != 1 {
		t.Fatalf("richTranscript returned %d frames, want 1", len(frames))
	}
	if frames[0].Seq != 1 || frames[0].Kind != proto.EventKindUserInputRequest ||
		frames[0].RequestID != "ui-1" || frames[0].Question != "Pick one" ||
		len(frames[0].Choices) != 2 || frames[0].Choices[1] != "B" {
		t.Fatalf("prompt frame = %+v", frames[0])
	}

	select {
	case raw := <-sub.ch:
		var live proto.EventFrame
		if err := json.Unmarshal(raw, &live); err != nil {
			t.Fatalf("live frame unmarshal: %v", err)
		}
		if live.Kind != proto.EventKindUserInputRequest || live.RequestID != "ui-1" {
			t.Fatalf("live frame = %+v", live)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for live prompt frame")
	}
}

// TestSDKEventFramePermissionCompleted asserts the SDK permission-completion event
// maps onto a permission.resolved frame with the right Decision (v18), so an
// answered card is dismissed (not re-shown) after a restart. Covers the approve
// variants, the deny/cancel variants, and a nil result (all -> reject).
func TestSDKEventFramePermissionCompleted(t *testing.T) {
	cases := []struct {
		name    string
		result  copilot.PermissionResult
		wantDec string
	}{
		{"approved", copilot.PermissionApproved{}, proto.DecisionApprove},
		{"approved-for-session", copilot.PermissionApprovedForSession{}, proto.DecisionApprove},
		{"approved-for-location", copilot.PermissionApprovedForLocation{}, proto.DecisionApprove},
		{"denied-interactively", copilot.PermissionDeniedInteractivelyByUser{}, proto.DecisionReject},
		{"denied-by-rules", copilot.PermissionDeniedByRules{}, proto.DecisionReject},
		{"cancelled", copilot.PermissionCancelled{}, proto.DecisionReject},
		{"nil-result", nil, proto.DecisionReject},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			frame, ok := sdkEventFrame(copilot.SessionEvent{Data: &copilot.PermissionCompletedData{
				RequestID: "perm-1",
				Result:    tc.result,
			}})
			if !ok {
				t.Fatal("sdkEventFrame did not map PermissionCompletedData")
			}
			if frame.Kind != proto.EventKindPermissionResolved || frame.RequestID != "perm-1" || frame.Decision != tc.wantDec {
				t.Fatalf("permission.resolved frame = %+v, want decision=%q", frame, tc.wantDec)
			}
		})
	}
}

// TestSDKEventFrameInputCompleted asserts the SDK user-input and elicitation
// completion events both map onto an input.resolved frame carrying the request id
// (v18), so the matching prompt UI is dismissed on resume.
func TestSDKEventFrameInputCompleted(t *testing.T) {
	frame, ok := sdkEventFrame(copilot.SessionEvent{Data: &copilot.UserInputCompletedData{RequestID: "ui-1"}})
	if !ok || frame.Kind != proto.EventKindInputResolved || frame.RequestID != "ui-1" {
		t.Fatalf("user_input.completed frame = %+v ok=%v", frame, ok)
	}
	frame, ok = sdkEventFrame(copilot.SessionEvent{Data: &copilot.ElicitationCompletedData{RequestID: "el-1"}})
	if !ok || frame.Kind != proto.EventKindInputResolved || frame.RequestID != "el-1" {
		t.Fatalf("elicitation.completed frame = %+v ok=%v", frame, ok)
	}
}

// TestSDKEventFrameModelChange asserts a live SDK model-change event maps onto a
// model frame carrying the new model plus the dereferenced effort and context tier
// (v18), and that nil effort/tier pointers deref to empty (omitted on the wire).
func TestSDKEventFrameModelChange(t *testing.T) {
	effort := "high"
	tier := copilot.ContextTierLongContext
	frame, ok := sdkEventFrame(copilot.SessionEvent{Data: &copilot.SessionModelChangeData{
		NewModel:        "gpt-5",
		ReasoningEffort: &effort,
		ContextTier:     &tier,
	}})
	if !ok {
		t.Fatal("sdkEventFrame did not map SessionModelChangeData")
	}
	if frame.Kind != proto.EventKindModel || frame.Model != "gpt-5" || frame.Effort != "high" || frame.ContextTier != "long_context" {
		t.Fatalf("model frame = %+v", frame)
	}

	bare, ok := sdkEventFrame(copilot.SessionEvent{Data: &copilot.SessionModelChangeData{NewModel: "claude"}})
	if !ok || bare.Model != "claude" || bare.Effort != "" || bare.ContextTier != "" {
		t.Fatalf("bare model frame = %+v ok=%v", bare, ok)
	}
}

// TestTranslateAndEmitResolutionFrames proves the completion events route through
// onSDKEvent (the live and resume-replay path) onto resolution frames, so an
// answered permission/input is dismissed on resume instead of re-prompting.
func TestTranslateAndEmitResolutionFrames(t *testing.T) {
	s := newSDKSession(sdkSessionParams{name: "rich-resolve", program: "copilot", workDir: t.TempDir()}, nil, nil)
	defer s.close()

	s.onSDKEvent(copilot.SessionEvent{Data: &copilot.PermissionCompletedData{RequestID: "perm-1", Result: copilot.PermissionApproved{}}})
	s.onSDKEvent(copilot.SessionEvent{Data: &copilot.UserInputCompletedData{RequestID: "ui-1"}})

	frames := s.richTranscript(0)
	if len(frames) != 2 {
		t.Fatalf("richTranscript len = %d, want 2", len(frames))
	}
	if frames[0].Kind != proto.EventKindPermissionResolved || frames[0].RequestID != "perm-1" || frames[0].Decision != proto.DecisionApprove {
		t.Fatalf("permission.resolved frame = %+v", frames[0])
	}
	if frames[1].Kind != proto.EventKindInputResolved || frames[1].RequestID != "ui-1" {
		t.Fatalf("input.resolved frame = %+v", frames[1])
	}
}

// TestEmitModelFrame asserts emitModelFrame emits one model frame carrying the
// session's current model (seeded from the v18 newSDKSession params) plus the
// persisted effort and tier, and is a no-op when no model is known (a fresh chat).
func TestEmitModelFrame(t *testing.T) {
	s := newSDKSession(sdkSessionParams{name: "rich-model", program: "copilot", workDir: t.TempDir(), model: "gpt-5", effort: "high", contextTier: "long_context"}, nil, nil)
	defer s.close()

	s.emitModelFrame()
	frames := s.richTranscript(0)
	if len(frames) != 1 {
		t.Fatalf("richTranscript len = %d, want 1", len(frames))
	}
	if f := frames[0]; f.Kind != proto.EventKindModel || f.Model != "gpt-5" || f.Effort != "high" || f.ContextTier != "long_context" {
		t.Fatalf("model frame = %+v", f)
	}

	bare := newSDKSession(sdkSessionParams{name: "rich-bare", program: "copilot", workDir: t.TempDir()}, nil, nil)
	defer bare.close()
	bare.emitModelFrame()
	if got := bare.richTranscript(0); len(got) != 0 {
		t.Fatalf("emitModelFrame with no model should emit nothing, got %+v", got)
	}
}

// TestSDKEventFrameReasoningDelta asserts the SDK incremental reasoning event maps onto
// an assistant.reasoning.delta frame carrying the chunk in Text (v19) so the desktop can
// grow the "thinking" block live, while the complete-block AssistantReasoningData still
// maps onto the assistant.reasoning finalizer frame.
func TestSDKEventFrameReasoningDelta(t *testing.T) {
	delta, ok := sdkEventFrame(copilot.SessionEvent{Data: &copilot.AssistantReasoningDeltaData{
		DeltaContent: "thin",
		ReasoningID:  "r1",
	}})
	if !ok {
		t.Fatal("sdkEventFrame did not map AssistantReasoningDeltaData")
	}
	if delta.Kind != proto.EventKindReasoningDelta || delta.Text != "thin" {
		t.Fatalf("reasoning delta frame = %+v, want kind=%q text=%q", delta, proto.EventKindReasoningDelta, "thin")
	}

	full, ok := sdkEventFrame(copilot.SessionEvent{Data: &copilot.AssistantReasoningData{
		Content:     "thinking",
		ReasoningID: "r1",
	}})
	if !ok {
		t.Fatal("sdkEventFrame did not map AssistantReasoningData")
	}
	if full.Kind != proto.EventKindReasoning || full.Text != "thinking" {
		t.Fatalf("reasoning finalizer frame = %+v, want kind=%q text=%q", full, proto.EventKindReasoning, "thinking")
	}
}

// TestTurnDangling asserts turnDangling folds a rich log exactly like the desktop's
// turnInProgress: a working frame with no following terminal => dangling.
func TestTurnDangling(t *testing.T) {
	cases := []struct {
		name string
		log  []proto.EventFrame
		want bool
	}{
		{"empty", nil, false},
		{"ends tool.start", []proto.EventFrame{{Kind: proto.EventKindToolStart}}, true},
		{"tool.start then complete (complete ignored)", []proto.EventFrame{{Kind: proto.EventKindToolStart}, {Kind: proto.EventKindToolComplete}}, true},
		{"reasoning delta only", []proto.EventFrame{{Kind: proto.EventKindReasoningDelta}}, true},
		{"working then ignored frames", []proto.EventFrame{{Kind: proto.EventKindToolStart}, {Kind: proto.EventKindUsage}, {Kind: proto.EventKindModel}}, true},
		{"permission requested pending", []proto.EventFrame{{Kind: proto.EventKindPermissionRequest}}, true},
		{"ends assistant.message", []proto.EventFrame{{Kind: proto.EventKindToolStart}, {Kind: proto.EventKindAssistantMessage}}, false},
		{"ends idle", []proto.EventFrame{{Kind: proto.EventKindReasoning}, {Kind: proto.EventKindIdle}}, false},
		{"ends error", []proto.EventFrame{{Kind: proto.EventKindToolStart}, {Kind: proto.EventKindError}}, false},
	}
	for _, tc := range cases {
		if got := turnDangling(tc.log); got != tc.want {
			t.Errorf("%s: turnDangling = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// TestEmitIdleIfDanglingAppendsIdle asserts a revived session whose log ends mid-turn
// gets a synthetic, timestamp-less idle frame so the desktop resets turnInProgress.
func TestEmitIdleIfDanglingAppendsIdle(t *testing.T) {
	s := newSDKSession(sdkSessionParams{name: "rich-dangle", program: "copilot", workDir: t.TempDir()}, nil, nil)
	defer s.close()

	s.emitFrame(proto.EventFrame{Kind: proto.EventKindToolStart, ToolName: "bash"})
	s.emitIdleIfDangling()

	frames := s.richTranscript(0)
	if len(frames) != 2 {
		t.Fatalf("richTranscript len = %d, want 2 (tool.start + synthetic idle)", len(frames))
	}
	if frames[1].Kind != proto.EventKindIdle || frames[1].Timestamp != 0 || frames[1].Aborted {
		t.Fatalf("settled frame = %+v, want a timestamp-less, non-aborted idle", frames[1])
	}
}

// TestEmitIdleIfDanglingNoopWhenTerminal asserts no synthetic idle is added when the
// replayed log already ended cleanly (no duplicate marker).
func TestEmitIdleIfDanglingNoopWhenTerminal(t *testing.T) {
	s := newSDKSession(sdkSessionParams{name: "rich-clean", program: "copilot", workDir: t.TempDir()}, nil, nil)
	defer s.close()

	s.emitFrame(proto.EventFrame{Kind: proto.EventKindToolStart, ToolName: "bash"})
	s.emitFrame(proto.EventFrame{Kind: proto.EventKindAssistantMessage, Text: "done"})
	s.emitIdleIfDangling()

	if frames := s.richTranscript(0); len(frames) != 2 {
		t.Fatalf("richTranscript len = %d, want 2 (no synthetic idle appended)", len(frames))
	}
}
