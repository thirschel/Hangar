//go:build windows

package winhost

import (
	"strings"
	"testing"

	copilot "github.com/github/copilot-sdk/go"
	"hangar/session/copilotsdk"
	"hangar/session/winhost/proto"
)

func TestModelInfosMapping(t *testing.T) {
	details := []copilotsdk.ModelDetail{
		{ID: "gpt-5", Name: "GPT-5"},
		{ID: "claude-sonnet-4.5"},
	}
	got := modelInfos(details)
	if len(got) != 2 {
		t.Fatalf("modelInfos len = %d, want 2", len(got))
	}
	if got[0] != (proto.ModelInfo{ID: "gpt-5", Name: "GPT-5"}) {
		t.Fatalf("model0 = %+v", got[0])
	}
	if got[1] != (proto.ModelInfo{ID: "claude-sonnet-4.5"}) {
		t.Fatalf("model1 = %+v", got[1])
	}
	if modelInfos(nil) != nil {
		t.Fatal("modelInfos(nil) should be nil")
	}
}

// TestUsageFrameMapping asserts a context-usage event maps into one usage frame
// carrying the model plus the token counts the desktop renders as context percent.
func TestUsageFrameMapping(t *testing.T) {
	frame := usageFrame(&copilot.SessionUsageInfoData{CurrentTokens: 12000, TokenLimit: 200000}, "gpt-5")
	if frame.Kind != proto.EventKindUsage || frame.Model != "gpt-5" ||
		frame.CurrentTokens != 12000 || frame.TokenLimit != 200000 {
		t.Fatalf("usage frame = %+v", frame)
	}
}

// TestEmitUsageSnapshot drives the usage emitter and asserts a single usage frame
// lands on the rich event stream.
func TestEmitUsageSnapshot(t *testing.T) {
	s := newSDKSession("rich-usage", "copilot", t.TempDir(), "", false, "", nil, nil)
	defer s.close()

	s.emitUsageSnapshot(&copilot.SessionUsageInfoData{CurrentTokens: 5000, TokenLimit: 128000}, "claude-sonnet-4.5")

	frames := s.richTranscript(0)
	if len(frames) != 1 {
		t.Fatalf("richTranscript len = %d, want 1", len(frames))
	}
	f := frames[0]
	if f.Kind != proto.EventKindUsage || f.Model != "claude-sonnet-4.5" ||
		f.CurrentTokens != 5000 || f.TokenLimit != 128000 {
		t.Fatalf("usage frame = %+v", f)
	}
}

func TestEmitUsageSnapshotNilNoFrame(t *testing.T) {
	s := newSDKSession("rich-usage", "copilot", t.TempDir(), "", false, "", nil, nil)
	defer s.close()

	s.emitUsageSnapshot(nil, "gpt-5")
	if frames := s.richTranscript(0); len(frames) != 0 {
		t.Fatalf("nil usage should emit no frame, got %+v", frames)
	}
}

// TestTranslateAndEmitUsageFrame proves a synthetic SessionUsageInfoData routed
// through onSDKEvent emits exactly one usage frame on the rich event stream. The
// model is empty here because onSDKEvent is downstream of copilotsdk capture.
func TestTranslateAndEmitUsageFrame(t *testing.T) {
	s := newSDKSession("rich-usage-route", "copilot", t.TempDir(), "", false, "", nil, nil)
	defer s.close()

	s.onSDKEvent(copilot.SessionEvent{Data: &copilot.SessionUsageInfoData{CurrentTokens: 9000, TokenLimit: 200000}})

	frames := s.richTranscript(0)
	if len(frames) != 1 {
		t.Fatalf("richTranscript len = %d, want 1", len(frames))
	}
	if frames[0].Kind != proto.EventKindUsage || frames[0].CurrentTokens != 9000 || frames[0].TokenLimit != 200000 {
		t.Fatalf("usage frame = %+v", frames[0])
	}
}

// TestModelMethodsRoutingErrors verifies dispatch routes the v14 model methods to
// the rich-session resolver: a missing session and a terminal (non-rich) session
// both produce the expected scoped errors, just like SendMessage.
func TestModelMethodsRoutingErrors(t *testing.T) {
	_, h, cleanup := startTestHostWithHandle(t)
	defer cleanup()

	if resp := h.dispatch(&proto.Request{ID: 1, Method: proto.MethodListModels, Session: "ghost"}); resp.OK ||
		!strings.Contains(resp.Error, "no such session") {
		t.Fatalf("ListModels(ghost) = %+v, want no-such-session error", resp)
	}
	if resp := h.dispatch(&proto.Request{ID: 2, Method: proto.MethodSetModel, Session: "ghost", Model: "gpt-5"}); resp.OK ||
		!strings.Contains(resp.Error, "no such session") {
		t.Fatalf("SetModel(ghost) = %+v, want no-such-session error", resp)
	}

	f := newFake("term", "copilot", "", "cmd", 80, 24, false, nil)
	h.mu.Lock()
	h.sessions["term"] = f
	h.mu.Unlock()
	if resp := h.dispatch(&proto.Request{ID: 3, Method: proto.MethodListModels, Session: "term"}); resp.OK ||
		!strings.Contains(resp.Error, "not a rich session") {
		t.Fatalf("ListModels(term) = %+v, want not-a-rich-session error", resp)
	}
	if resp := h.dispatch(&proto.Request{ID: 4, Method: proto.MethodSetModel, Session: "term", Model: "gpt-5"}); resp.OK ||
		!strings.Contains(resp.Error, "not a rich session") {
		t.Fatalf("SetModel(term) = %+v, want not-a-rich-session error", resp)
	}
}

// TestModelMethodsRouteToRichSession proves the v14 methods resolve a rich
// (sdkSession) via the same path as SendMessage and invoke the SDK adapter; an
// unstarted session surfaces the SDK not-started error (no CLI is spawned).
func TestModelMethodsRouteToRichSession(t *testing.T) {
	_, h, cleanup := startTestHostWithHandle(t)
	defer cleanup()

	rich := newSDKSession("rich-route", "copilot", t.TempDir(), "", false, "", nil, nil)
	defer rich.close()
	h.mu.Lock()
	h.sessions["rich-route"] = rich
	h.mu.Unlock()

	resp := h.dispatch(&proto.Request{ID: 1, Method: proto.MethodListModels, Session: "rich-route"})
	if resp.OK || !strings.Contains(resp.Error, "list models:") || !strings.Contains(resp.Error, "not started") {
		t.Fatalf("ListModels(rich) = %+v, want list-models not-started error", resp)
	}
	resp = h.dispatch(&proto.Request{ID: 2, Method: proto.MethodSetModel, Session: "rich-route", Model: "gpt-5"})
	if resp.OK || !strings.Contains(resp.Error, "set model:") || !strings.Contains(resp.Error, "not started") {
		t.Fatalf("SetModel(rich) = %+v, want set-model not-started error", resp)
	}
}
