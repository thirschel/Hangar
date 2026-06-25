//go:build windows

package winhost

import (
	"context"
	"strings"
	"testing"

	copilot "github.com/github/copilot-sdk/go"
)

// TestSDKSessionAdapter exercises the managedSession mapping that does not need a
// live Copilot runtime (start() is covered by the package's e2e tests).
func TestSDKSessionAdapter(t *testing.T) {
	s := newSDKSession("ws1", "copilot", t.TempDir(), "", true, "", "", "", "", nil, nil)

	var ms managedSession = s // also enforced by the package-level assertion
	if ms == nil {
		t.Fatal("nil managedSession")
	}

	if info := s.info(); info.Name != "ws1" || info.Program != "copilot" || !info.Alive {
		t.Errorf("info = %+v", info)
	}
	if !s.alive() {
		t.Error("should be alive before close")
	}
	if busy, waiting := s.agentStatus(); busy || waiting {
		t.Errorf("loading session should be neither busy nor waiting, got busy=%v waiting=%v", busy, waiting)
	}
	if s.bracketedPasteEnabled() {
		t.Error("bracketed paste should be false")
	}
	if got := s.capture(true, true); got != "" {
		t.Errorf("capture should be empty for a rich session, got %q", got)
	}
	if _, ok, n := s.captureHistory(true, 80, 24); ok || n != 0 {
		t.Errorf("captureHistory should be empty, got ok=%v n=%d", ok, n)
	}
	if err := s.sendKeys([]byte("x")); err != nil {
		t.Errorf("sendKeys no-op returned err: %v", err)
	}
	if err := s.resize(100, 40); err != nil {
		t.Errorf("resize no-op returned err: %v", err)
	}

	// subscribe/unsubscribe must not panic and must yield a usable subscriber.
	snap, sub := s.subscribe(80, 24)
	if snap != nil {
		t.Errorf("rich snapshot should be nil, got %v", snap)
	}
	if sub == nil || sub.ch == nil {
		t.Fatal("subscribe must return a usable subscriber")
	}
	s.unsubscribe(sub)

	s.setAutoYes(false)
	s.setAutoYes(true)
	if err := s.richRespondPermission(context.Background(), "perm-1", true); err == nil || !strings.Contains(err.Error(), "session not started") {
		t.Fatalf("richRespondPermission error = %v, want not-started error", err)
	}
	if err := s.richRespondUserInput("ui-1", "answer", true); err == nil || !strings.Contains(err.Error(), "no pending user input") {
		t.Fatalf("richRespondUserInput error = %v, want no-pending error", err)
	}

	if err := s.close(); err != nil {
		t.Errorf("close returned err: %v", err)
	}
	if s.alive() {
		t.Error("should not be alive after close")
	}
	if err := s.close(); err != nil {
		t.Errorf("double close should be a no-op, got %v", err)
	}
}

func TestSDKSessionEmitsPendingMCPStatusFrames(t *testing.T) {
	s := newSDKSession("rich-mcp", "copilot", t.TempDir(), "", false, "", "", "", "", nil, nil)
	defer s.close()

	s.emitMCPServerPendingFrames([]string{"github", "", "docs"})

	frames := s.richTranscript(0)
	if len(frames) != 2 {
		t.Fatalf("richTranscript returned %d frames, want 2", len(frames))
	}
	for i, want := range []string{"github", "docs"} {
		if frames[i].Kind != "mcp.status" || frames[i].MCPServer != want || frames[i].Status != "pending" {
			t.Fatalf("pending MCP frame %d = %+v", i, frames[i])
		}
	}
}

func TestSDKSessionBuffersStartupMCPStatusUntilPending(t *testing.T) {
	s := newSDKSession("rich-mcp", "copilot", t.TempDir(), "", false, "", "", "", "", nil, nil)
	defer s.close()

	s.beginMCPStartupBuffer()
	s.onSDKEvent(copilot.SessionEvent{Data: &copilot.SessionMCPServerStatusChangedData{
		ServerName: "github",
		Status:     copilot.MCPServerStatusConnected,
	}})
	if frames := s.richTranscript(0); len(frames) != 0 {
		t.Fatalf("buffered startup MCP event emitted early: %+v", frames)
	}

	s.emitMCPServerPendingFrames([]string{"github"})
	s.flushMCPStartupBuffer()

	frames := s.richTranscript(0)
	if len(frames) != 2 {
		t.Fatalf("richTranscript returned %d frames, want 2", len(frames))
	}
	if frames[0].Status != "pending" || frames[1].Status != string(copilot.MCPServerStatusConnected) {
		t.Fatalf("startup MCP frame order = %+v", frames)
	}
}

func TestSDKSessionResumeReplaySkipsHistoricalMCPStatus(t *testing.T) {
	s := newSDKSession("rich-mcp", "copilot", t.TempDir(), "", false, "", "", "", "", nil, nil)
	defer s.close()

	stale := copilot.SessionEvent{Data: &copilot.SessionMCPServerStatusChangedData{
		ServerName: "github",
		Status:     copilot.MCPServerStatusConnected,
	}}
	s.emitMCPServerPendingFrames([]string{"github"})
	if !isMCPStatusEvent(stale) {
		t.Fatal("status-changed event should be filtered during resume replay")
	}
	if !isMCPStatusEvent(copilot.SessionEvent{Data: &copilot.SessionMCPServersLoadedData{}}) {
		t.Fatal("servers-loaded event should be filtered during resume replay")
	}
	nonMCP := copilot.SessionEvent{Data: &copilot.AssistantMessageData{Content: "hello"}}
	if isMCPStatusEvent(nonMCP) {
		t.Fatal("assistant message should not be filtered during resume replay")
	}
	s.translateAndEmit(nonMCP)

	frames := s.richTranscript(0)
	if len(frames) != 2 {
		t.Fatalf("richTranscript returned %d frames, want 2", len(frames))
	}
	if frames[0].MCPServer != "github" || frames[0].Status != "pending" {
		t.Fatalf("pending MCP frame = %+v", frames[0])
	}
	if frames[1].Kind != "assistant.message" {
		t.Fatalf("non-MCP replay frame = %+v", frames[1])
	}
}
