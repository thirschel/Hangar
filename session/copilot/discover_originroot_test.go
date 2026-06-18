package copilot

import (
	"os"
	"path/filepath"
	"testing"
)

// TestValidateOriginRoot exercises the F-03 OriginRoot/gitRoot vetting that runs
// over untrusted Copilot state (workspace.yaml git_root and events.jsonl
// context.gitRoot). Structurally bogus values must degrade to "" so they are
// never handed to `git worktree add`; a genuine not-yet-present absolute path is
// preserved for the authoritative resume-time gate to re-validate.
func TestValidateOriginRoot(t *testing.T) {
	// A real git repo root (carries a .git directory).
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatalf("setup repo: %v", err)
	}

	// An existing directory that is NOT a git repo.
	notRepo := t.TempDir()

	// An existing path that is a file, not a directory.
	filePath := filepath.Join(t.TempDir(), "afile")
	if err := os.WriteFile(filePath, []byte("x"), 0o644); err != nil {
		t.Fatalf("setup file: %v", err)
	}

	// A not-yet-present absolute path (e.g. an unmounted drive). Build it in an
	// OS-appropriate way so the test runs cross-platform.
	missingAbs := filepath.Join(t.TempDir(), "does", "not", "exist")

	cases := []struct {
		name string
		in   string
		want string
	}{
		{"empty", "", ""},
		{"whitespace", "   ", ""},
		{"relative", filepath.Join("rel", "path"), ""},
		{"option-like", "--output=evil", ""},
		{"real-repo", repo, filepath.Clean(repo)},
		{"exists-not-repo", notRepo, ""},
		{"exists-is-file", filePath, ""},
		{"missing-absolute-preserved", missingAbs, filepath.Clean(missingAbs)},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := validateOriginRoot(tc.in)
			if got != tc.want {
				t.Fatalf("validateOriginRoot(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
