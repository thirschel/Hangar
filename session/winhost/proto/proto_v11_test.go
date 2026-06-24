package proto

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestEventFrameRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	frame := EventFrame{
		Seq:     42,
		Kind:    EventKindAssistantMessage,
		Text:    "hello",
		Choices: []string{"yes", "no"},
	}

	if err := WriteFrame(&buf, frame); err != nil {
		t.Fatalf("WriteFrame: %v", err)
	}
	b, err := ReadFrameBytes(&buf)
	if err != nil {
		t.Fatalf("ReadFrameBytes: %v", err)
	}

	var got EventFrame
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if got.Seq != frame.Seq || got.Kind != frame.Kind || got.Text != frame.Text {
		t.Fatalf("round-trip mismatch: %+v", got)
	}
	if len(got.Choices) != len(frame.Choices) || got.Choices[0] != "yes" || got.Choices[1] != "no" {
		t.Fatalf("choices round-trip mismatch: %+v", got.Choices)
	}
}

func TestRichMethodConstants(t *testing.T) {
	methods := map[string]string{
		"MethodOpenRichStream": MethodOpenRichStream,
		"MethodSendMessage":    MethodSendMessage,
		"MethodAbortTurn":      MethodAbortTurn,
		"MethodGetTranscript":  MethodGetTranscript,
	}
	want := map[string]string{
		"MethodOpenRichStream": "OpenRichStream",
		"MethodSendMessage":    "SendMessage",
		"MethodAbortTurn":      "AbortTurn",
		"MethodGetTranscript":  "GetTranscript",
	}
	for name, got := range methods {
		if got != want[name] {
			t.Fatalf("%s = %q, want %q", name, got, want[name])
		}
	}
}

func TestRichEventKindConstantsAndEnvelopeFields(t *testing.T) {
	kinds := map[string]string{
		"EventKindAssistantMessage":  EventKindAssistantMessage,
		"EventKindAssistantDelta":    EventKindAssistantDelta,
		"EventKindReasoning":         EventKindReasoning,
		"EventKindToolStart":         EventKindToolStart,
		"EventKindToolComplete":      EventKindToolComplete,
		"EventKindPermissionRequest": EventKindPermissionRequest,
		"EventKindUserInputRequest":  EventKindUserInputRequest,
		"EventKindUsage":             EventKindUsage,
		"EventKindTitle":             EventKindTitle,
		"EventKindIdle":              EventKindIdle,
		"EventKindError":             EventKindError,
	}
	want := map[string]string{
		"EventKindAssistantMessage":  "assistant.message",
		"EventKindAssistantDelta":    "assistant.delta",
		"EventKindReasoning":         "assistant.reasoning",
		"EventKindToolStart":         "tool.start",
		"EventKindToolComplete":      "tool.complete",
		"EventKindPermissionRequest": "permission.requested",
		"EventKindUserInputRequest":  "user_input.requested",
		"EventKindUsage":             "usage",
		"EventKindTitle":             "title",
		"EventKindIdle":              "idle",
		"EventKindError":             "error",
	}
	for name, got := range kinds {
		if got != want[name] {
			t.Fatalf("%s = %q, want %q", name, got, want[name])
		}
	}

	req := Request{Since: 7}
	resp := Response{Frames: []EventFrame{{Seq: 8, Kind: EventKindIdle}}}
	if req.Since != 7 || len(resp.Frames) != 1 || resp.Frames[0].Kind != EventKindIdle {
		t.Fatalf("rich envelope fields mismatch: req=%+v resp=%+v", req, resp)
	}
}
