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
	err := s.SetModel(context.Background(), "gpt-5", "", "")
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

// TestSetModelOptions covers the v16 effort+tier -> *copilot.SetModelOptions
// mapping: both-empty -> nil (byte-for-byte the v14 nil-options behavior),
// effort-only, the context-tier string -> SDK enum cases, an unknown/empty tier
// ignored defensively, and both fields set together.
func TestSetModelOptions(t *testing.T) {
	if opts := setModelOptions("", ""); opts != nil {
		t.Fatalf(`setModelOptions("","") = %+v, want nil (v14 behavior)`, opts)
	}

	// Effort only: ReasoningEffort set, ContextTier left nil.
	opts := setModelOptions("high", "")
	if opts == nil || opts.ReasoningEffort == nil || *opts.ReasoningEffort != "high" {
		t.Fatalf("effort-only ReasoningEffort = %+v, want high", opts)
	}
	if opts.ContextTier != nil {
		t.Fatalf("effort-only ContextTier = %v, want nil", opts.ContextTier)
	}

	// Tier "default" -> ContextTierDefault, effort left nil.
	opts = setModelOptions("", "default")
	if opts == nil || opts.ContextTier == nil || *opts.ContextTier != copilot.ContextTierDefault {
		t.Fatalf("tier=default ContextTier = %+v, want ContextTierDefault", opts)
	}
	if opts.ReasoningEffort != nil {
		t.Fatalf("tier-only ReasoningEffort = %v, want nil", opts.ReasoningEffort)
	}

	// Tier "long_context" -> ContextTierLongContext.
	opts = setModelOptions("", "long_context")
	if opts == nil || opts.ContextTier == nil || *opts.ContextTier != copilot.ContextTierLongContext {
		t.Fatalf("tier=long_context ContextTier = %+v, want ContextTierLongContext", opts)
	}

	// An unknown tier alone is ignored defensively -> nil options.
	if opts := setModelOptions("", "galaxy"); opts != nil {
		t.Fatalf("unknown tier alone = %+v, want nil", opts)
	}
	// An unknown tier alongside an effort keeps the effort and leaves ContextTier nil.
	opts = setModelOptions("low", "galaxy")
	if opts == nil || opts.ReasoningEffort == nil || *opts.ReasoningEffort != "low" || opts.ContextTier != nil {
		t.Fatalf("effort + unknown tier = %+v, want effort=low tier=nil", opts)
	}

	// Both set: effort + tier together.
	opts = setModelOptions("xhigh", "long_context")
	if opts == nil || opts.ReasoningEffort == nil || *opts.ReasoningEffort != "xhigh" ||
		opts.ContextTier == nil || *opts.ContextTier != copilot.ContextTierLongContext {
		t.Fatalf("effort+tier = %+v, want xhigh + long_context", opts)
	}
}

// TestModelDetailMapping covers the v16 SDK ModelInfo -> ModelDetail field carry
// used by ListModels: id/name plus the reasoning-effort options, with
// SupportedEfforts a defensive copy of the SDK slice.
func TestModelDetailMapping(t *testing.T) {
	efforts := []string{"low", "medium", "high"}
	d := modelDetail(copilot.ModelInfo{
		ID:                        "gpt-5",
		Name:                      "GPT-5",
		SupportedReasoningEfforts: efforts,
		DefaultReasoningEffort:    "medium",
	})
	if d.ID != "gpt-5" || d.Name != "GPT-5" || d.DefaultEffort != "medium" {
		t.Fatalf("modelDetail = %+v", d)
	}
	if len(d.SupportedEfforts) != 3 || d.SupportedEfforts[0] != "low" ||
		d.SupportedEfforts[1] != "medium" || d.SupportedEfforts[2] != "high" {
		t.Fatalf("modelDetail SupportedEfforts = %v", d.SupportedEfforts)
	}
	// Defensive copy: mutating the result must not change the SDK source slice.
	d.SupportedEfforts[0] = "mutated"
	if efforts[0] != "low" {
		t.Fatalf("modelDetail aliased the SDK slice: %v", efforts)
	}

	// A model with no reasoning effort yields empty (nil) effort fields.
	bare := modelDetail(copilot.ModelInfo{ID: "claude", Name: "Claude"})
	if bare.SupportedEfforts != nil || bare.DefaultEffort != "" {
		t.Fatalf("bare modelDetail efforts = %+v, want empty", bare)
	}
}
