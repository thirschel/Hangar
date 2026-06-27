//go:build windows

package winhost

import (
	"context"
	"testing"
	"time"

	"hangar/session/winhost/proto"
)

// TestSendMessageThreadsAttachmentsToRichSend proves a v15 MethodSendMessage
// request's Attachments reach sdkSession.richSend (and onward to the SDK adapter).
// The send runs on a goroutine, so we intercept the SDK boundary with a capturing
// sendFn and synchronize on it; no live Copilot CLI is required.
func TestSendMessageThreadsAttachmentsToRichSend(t *testing.T) {
	_, h, cleanup := startTestHostWithHandle(t)
	defer cleanup()

	rich := newSDKSession(sdkSessionParams{name: "rich-attach", program: "copilot", workDir: t.TempDir()}, nil, nil)
	defer rich.close()

	type call struct {
		text        string
		attachments []string
	}
	got := make(chan call, 1)
	rich.sendFn = func(_ context.Context, text string, attachments []string, _ string) error {
		got <- call{text: text, attachments: attachments}
		return nil
	}

	h.mu.Lock()
	h.sessions["rich-attach"] = rich
	h.mu.Unlock()

	resp := h.dispatch(&proto.Request{
		ID:          1,
		Method:      proto.MethodSendMessage,
		Session:     "rich-attach",
		Message:     "review these",
		Attachments: []string{`C:\a.txt`, `C:\b.png`},
	})
	if !resp.OK {
		t.Fatalf("SendMessage dispatch = %+v, want OK", resp)
	}

	select {
	case c := <-got:
		if c.text != "review these" {
			t.Fatalf("richSend text = %q, want %q", c.text, "review these")
		}
		if len(c.attachments) != 2 || c.attachments[0] != `C:\a.txt` || c.attachments[1] != `C:\b.png` {
			t.Fatalf(`richSend attachments = %#v, want [C:\a.txt C:\b.png]`, c.attachments)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("richSend was not called within 2s")
	}
}

// TestRichSendNilAttachmentsUnchanged proves the empty-attachments path threads a
// nil slice straight through richSend, preserving today's plain-message behavior.
func TestRichSendNilAttachmentsUnchanged(t *testing.T) {
	rich := newSDKSession(sdkSessionParams{name: "rich-plain", program: "copilot", workDir: t.TempDir()}, nil, nil)
	defer rich.close()

	got := make(chan []string, 1)
	rich.sendFn = func(_ context.Context, _ string, attachments []string, _ string) error {
		got <- attachments
		return nil
	}

	if err := rich.richSend(context.Background(), "hi", nil, ""); err != nil {
		t.Fatalf("richSend = %v", err)
	}
	if a := <-got; a != nil {
		t.Fatalf("richSend attachments = %#v, want nil", a)
	}
}
