package copilotsdk

import (
	"path/filepath"
	"testing"

	copilot "github.com/github/copilot-sdk/go"
)

// TestAttachmentsFromPaths proves Send's path->attachment mapping: each non-empty
// absolute path becomes a *copilot.AttachmentFile whose Path is forwarded as-is and
// whose DisplayName is the base name. Paths are built with filepath.Join so the
// expected base name is correct on every OS (the package is not Windows-only).
func TestAttachmentsFromPaths(t *testing.T) {
	p0 := filepath.Join("work", "main.go")
	p1 := filepath.Join("work", "img", "diagram.png")

	got := attachmentsFromPaths([]string{p0, "", p1})
	if len(got) != 2 {
		t.Fatalf("attachmentsFromPaths len = %d, want 2 (empty path skipped)", len(got))
	}

	f0, ok := got[0].(*copilot.AttachmentFile)
	if !ok {
		t.Fatalf("got[0] type = %T, want *copilot.AttachmentFile", got[0])
	}
	if f0.Path != p0 || f0.DisplayName != "main.go" {
		t.Fatalf("got[0] = {Path:%q DisplayName:%q}, want {%q main.go}", f0.Path, f0.DisplayName, p0)
	}

	f1, ok := got[1].(*copilot.AttachmentFile)
	if !ok {
		t.Fatalf("got[1] type = %T, want *copilot.AttachmentFile", got[1])
	}
	if f1.Path != p1 || f1.DisplayName != "diagram.png" {
		t.Fatalf("got[1] = {Path:%q DisplayName:%q}, want {%q diagram.png}", f1.Path, f1.DisplayName, p1)
	}
}

// TestAttachmentsFromPathsEmpty proves the unchanged plain-message path: a nil or
// all-empty slice yields nil, so MessageOptions.Attachments stays unset.
func TestAttachmentsFromPathsEmpty(t *testing.T) {
	if got := attachmentsFromPaths(nil); got != nil {
		t.Fatalf("attachmentsFromPaths(nil) = %#v, want nil", got)
	}
	if got := attachmentsFromPaths([]string{"", ""}); got != nil {
		t.Fatalf("attachmentsFromPaths(all-empty) = %#v, want nil", got)
	}
}
