package proto

import (
	"encoding/json"
	"testing"
)

func TestVersionV15(t *testing.T) {
	if Version != 15 {
		t.Fatalf("Version = %d, want 15", Version)
	}
}

// TestSendMessageAttachmentsRoundTrip guards the v15 wire shape: a SendMessage
// request carries Request.Attachments (absolute file paths) alongside the message
// text, and the field serializes under the "attachments" JSON key.
func TestSendMessageAttachmentsRoundTrip(t *testing.T) {
	req := Request{
		ID:          7,
		Method:      MethodSendMessage,
		Session:     "ws-1",
		Message:     "look at these",
		Attachments: []string{`C:\work\main.go`, `C:\work\img\diagram.png`},
	}
	b, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var got Request
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if got.Method != MethodSendMessage || got.Session != "ws-1" || got.Message != "look at these" {
		t.Fatalf("request round-trip = %+v", got)
	}
	if len(got.Attachments) != 2 ||
		got.Attachments[0] != `C:\work\main.go` ||
		got.Attachments[1] != `C:\work\img\diagram.png` {
		t.Fatalf("attachments round-trip = %#v", got.Attachments)
	}
	if !containsKey(b, "attachments") {
		t.Fatalf("expected attachments key on a SendMessage request, got %s", b)
	}
}

// TestAttachmentsOmittedWhenEmpty guards the additive contract: a SendMessage
// without attachments must NOT serialize the field, so the existing send path is
// byte-for-byte unchanged for old clients.
func TestAttachmentsOmittedWhenEmpty(t *testing.T) {
	b, err := json.Marshal(Request{ID: 1, Method: MethodSendMessage, Session: "ws", Message: "hi"})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if containsKey(b, "attachments") {
		t.Fatalf("expected attachments omitted on a plain message, got %s", b)
	}
}
