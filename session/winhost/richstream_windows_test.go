//go:build windows

package winhost

import (
	"encoding/json"
	"testing"
	"time"

	copilot "github.com/github/copilot-sdk/go"
	"hangar/session/copilotsdk"
	"hangar/session/winhost/proto"
)

func TestSDKSessionRichEventsAndReplay(t *testing.T) {
	s := newSDKSession("rich1", "copilot", t.TempDir(), "", false, "", nil, nil)
	defer s.close()

	mcpServer := "github"
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
		{Data: &copilot.PermissionRequestedData{RequestID: "perm1"}},
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
	if frames[3].Kind != proto.EventKindToolStart || frames[3].ToolName != "read_file" || frames[3].MCPServer != "github" {
		t.Fatalf("tool start frame = %+v", frames[3])
	}
	if frames[4].Kind != proto.EventKindToolComplete || frames[4].ToolName != "read_file" {
		t.Fatalf("tool complete frame = %+v", frames[4])
	}
	if frames[5].Kind != proto.EventKindPermissionRequest || frames[5].RequestID != "perm1" {
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

func TestOnSDKEventMCPStatusFrames(t *testing.T) {
	s := newSDKSession("rich-mcp", "copilot", t.TempDir(), "", false, "", nil, nil)
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

func TestSDKPromptEmitsUserInputFrame(t *testing.T) {
	s := newSDKSession("rich1", "copilot", t.TempDir(), "", false, "", nil, nil)
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
