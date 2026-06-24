//go:build windows

package winhost

import (
	"context"
	"strings"
	"testing"
)

// TestSDKSessionAdapter exercises the managedSession mapping that does not need a
// live Copilot runtime (start() is covered by the package's e2e tests).
func TestSDKSessionAdapter(t *testing.T) {
	s := newSDKSession("ws1", "copilot", t.TempDir(), "", true, "", nil, nil)

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
