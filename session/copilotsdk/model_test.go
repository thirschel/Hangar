package copilotsdk

import (
	"context"
	"strings"
	"testing"

	copilot "github.com/github/copilot-sdk/go"
)

func TestListModelsNotStarted(t *testing.T) {
	s := New(Config{})
	if _, err := s.ListModels(context.Background()); err == nil || !strings.Contains(err.Error(), "session not started") {
		t.Fatalf("ListModels error = %v, want not-started error", err)
	}
}

func TestSetModelNotStarted(t *testing.T) {
	s := New(Config{})
	err := s.SetModel(context.Background(), "gpt-5")
	if err == nil || !strings.Contains(err.Error(), "session not started") {
		t.Fatalf("SetModel error = %v, want not-started error", err)
	}
}

func TestCurrentModelSeededFromConfig(t *testing.T) {
	if got := New(Config{Model: "gpt-5"}).CurrentModel(); got != "gpt-5" {
		t.Fatalf("CurrentModel() = %q, want gpt-5 (seeded from config)", got)
	}
	if got := New(Config{}).CurrentModel(); got != "" {
		t.Fatalf("CurrentModel() = %q, want empty when config has no model", got)
	}
}

// TestCaptureUsageThroughHandleEvent drives a synthetic usage_info event through
// handleEvent and asserts the captured context-window usage is readable.
func TestCaptureUsageThroughHandleEvent(t *testing.T) {
	s := New(Config{})
	if _, _, known := s.Usage(); known {
		t.Fatal("Usage() should be unknown before any usage event")
	}
	s.handleEvent(copilot.SessionEvent{Data: &copilot.SessionUsageInfoData{CurrentTokens: 12000, TokenLimit: 200000}})
	cur, limit, known := s.Usage()
	if !known || cur != 12000 || limit != 200000 {
		t.Fatalf("Usage() = (%d, %d, %v), want (12000, 200000, true)", cur, limit, known)
	}
}

// TestCaptureModelChangeThroughHandleEvent asserts a model-change event updates the
// tracked current model, and that an empty NewModel never clobbers it.
func TestCaptureModelChangeThroughHandleEvent(t *testing.T) {
	s := New(Config{Model: "gpt-5"})
	s.handleEvent(copilot.SessionEvent{Data: &copilot.SessionModelChangeData{NewModel: "claude-sonnet-4.5"}})
	if got := s.CurrentModel(); got != "claude-sonnet-4.5" {
		t.Fatalf("CurrentModel() = %q, want claude-sonnet-4.5 after model change", got)
	}
	s.handleEvent(copilot.SessionEvent{Data: &copilot.SessionModelChangeData{NewModel: ""}})
	if got := s.CurrentModel(); got != "claude-sonnet-4.5" {
		t.Fatalf("CurrentModel() = %q, want unchanged after an empty model change", got)
	}
}
