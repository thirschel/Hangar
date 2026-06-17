package git

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestValidateSHA(t *testing.T) {
	tests := []struct {
		name    string
		sha     string
		wantErr bool
	}{
		{"valid short", "a1b2c3d", false},
		{"valid 40 hex", "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2", false},
		{"valid uppercase", "ABCDEF0", false},
		{"empty", "", true},
		{"too short 6", "a1b2c3", true},
		{"too long 41", strings.Repeat("a", 41), true},
		{"option injection --output", `--output=C:\evil\dump`, true},
		{"dotdot", "..", true},
		{"dotdot path", "../../etc", true},
		{"has space", "a1b2c3d e", true},
		{"non hex letters", "g1h2i3j", true},
		{"ref name", "HEAD", true},
		{"branch ref", "origin/main", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateSHA(tt.sha)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ValidateSHA(%q) err=%v, wantErr=%v", tt.sha, err, tt.wantErr)
			}
		})
	}
}

func TestStripExtendedLengthPrefix(t *testing.T) {
	cases := map[string]string{
		`\\?\C:\Users\x`:            `C:\Users\x`,
		`\\?\UNC\server\share\file`: `\\server\share\file`,
		`C:\Users\x`:                `C:\Users\x`,
		`/home/user/x`:              `/home/user/x`,
	}
	for in, want := range cases {
		if got := stripExtendedLengthPrefix(in); got != want {
			t.Errorf("stripExtendedLengthPrefix(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPathSegments(t *testing.T) {
	if runtime.GOOS == "windows" {
		got := pathSegments(`C:\Users\x\worktrees\a`)
		want := []string{"Users", "x", "worktrees", "a"}
		if strings.Join(got, "/") != strings.Join(want, "/") {
			t.Errorf("segments = %v, want %v", got, want)
		}
		if vol := filepath.VolumeName(`\\server\share\a\b`); vol != `\\server\share` {
			t.Errorf("UNC volume = %q", vol)
		}
	} else {
		got := pathSegments("/home/user/worktrees/a")
		want := []string{"home", "user", "worktrees", "a"}
		if strings.Join(got, "/") != strings.Join(want, "/") {
			t.Errorf("segments = %v, want %v", got, want)
		}
	}
}

// TestContainsCanonical exercises the pure volume+segment comparison directly
// with synthetic, already-canonicalised paths (no filesystem access), covering
// the Windows edge cases the reviewers flagged. Case-folding follows the host OS,
// so the Windows-spelled cases are gated on runtime.GOOS.
func TestContainsCanonical(t *testing.T) {
	if runtime.GOOS == "windows" {
		base := `C:\Users\x\.hangar\worktrees`
		win := []struct {
			name   string
			target string
			want   bool
		}{
			{"direct child", base + `\mybranch_abc`, true},
			{"nested child", base + `\a\b\c`, true},
			{"equal to base is not under", base, false},
			{"parent is not under", `C:\Users\x\.hangar`, false},
			{"system32 rejected", `C:\Windows\System32`, false},
			// ..foo prefix false positive: a string-prefix test would accept this
			// sibling; segment comparison rejects it.
			{"sibling prefix worktrees-evil rejected", `C:\Users\x\.hangar\worktrees-evil\a`, false},
			{"sibling suffix worktreesX rejected", `C:\Users\x\.hangar\worktreesX`, false},
			// ..foo as a legitimate child directory name (begins with .. but is a
			// real segment, not a traversal) must be accepted.
			{"legit child named ..foo", base + `\..foo`, true},
			// case-insensitive on Windows.
			{"case-insensitive child", strings.ToUpper(base) + `\Branch`, true},
			// different drive letter.
			{"different volume rejected", `D:\Users\x\.hangar\worktrees\a`, false},
			// extended-length spelling of a child (already stripped by canonicalize,
			// but verify the comparison itself is prefix-agnostic).
			{"forward slash child", base + `/altsep`, true},
		}
		for _, tt := range win {
			t.Run(tt.name, func(t *testing.T) {
				if got := containsCanonical(base, tt.target); got != tt.want {
					t.Errorf("containsCanonical(%q, %q) = %v, want %v", base, tt.target, got, tt.want)
				}
			})
		}

		// UNC base/target.
		uncBase := `\\server\share\worktrees`
		if !containsCanonical(uncBase, uncBase+`\child`) {
			t.Errorf("UNC child should be contained")
		}
		if containsCanonical(uncBase, `\\other\share\worktrees\child`) {
			t.Errorf("UNC different host should not be contained")
		}
		return
	}

	// POSIX (case-sensitive) cases.
	base := "/home/user/.hangar/worktrees"
	posix := []struct {
		name   string
		target string
		want   bool
	}{
		{"direct child", base + "/mybranch", true},
		{"equal not under", base, false},
		{"parent not under", "/home/user/.hangar", false},
		{"etc rejected", "/etc", false},
		{"sibling prefix rejected", "/home/user/.hangar/worktrees-evil/a", false},
		{"legit child ..foo", base + "/..foo", true},
		{"case-sensitive mismatch rejected", "/home/user/.hangar/Worktrees/a", false},
	}
	for _, tt := range posix {
		t.Run(tt.name, func(t *testing.T) {
			if got := containsCanonical(base, tt.target); got != tt.want {
				t.Errorf("containsCanonical(%q, %q) = %v, want %v", base, tt.target, got, tt.want)
			}
		})
	}
}

// TestIsUnderDirReal exercises the full canonicalising path (Abs + EvalSymlinks)
// against real on-disk directories.
func TestIsUnderDirReal(t *testing.T) {
	base := t.TempDir()
	child := filepath.Join(base, "branch_123")
	if err := os.MkdirAll(child, 0o755); err != nil {
		t.Fatal(err)
	}

	if under, err := IsUnderDir(base, child); err != nil || !under {
		t.Fatalf("child should be under base: under=%v err=%v", under, err)
	}
	// Not-yet-created child (pre-creation worktree path) still resolves via Abs.
	pre := filepath.Join(base, "not_created_yet_abc")
	if under, err := IsUnderDir(base, pre); err != nil || !under {
		t.Fatalf("pre-creation child should be under base: under=%v err=%v", under, err)
	}
	// base itself is not "under" base.
	if under, _ := IsUnderDir(base, base); under {
		t.Fatalf("base must not be under itself")
	}
	// A sibling directory sharing a name prefix is rejected.
	sibling := base + "-evil"
	if under, _ := IsUnderDir(base, sibling); under {
		t.Fatalf("sibling %q must not be under %q", sibling, base)
	}
}

// TestAssertUnderWorktreeDirRejectsSystemPath verifies a tampered worktree path
// pointing at a system directory is refused, so it can never reach RemoveAll.
func TestAssertUnderWorktreeDirRejectsSystemPath(t *testing.T) {
	base := t.TempDir()

	var evil string
	if runtime.GOOS == "windows" {
		evil = `C:\Windows\System32`
	} else {
		evil = "/etc"
	}
	err := AssertUnderWorktreeDir(base, evil)
	if err == nil {
		t.Fatalf("AssertUnderWorktreeDir(%q, %q) = nil, want error", base, evil)
	}
	if !strings.Contains(err.Error(), "outside the managed worktrees") {
		t.Fatalf("unexpected error: %v", err)
	}

	// A real child must pass.
	child := filepath.Join(base, "ok_child")
	if err := os.MkdirAll(child, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := AssertUnderWorktreeDir(base, child); err != nil {
		t.Fatalf("AssertUnderWorktreeDir(child) = %v, want nil", err)
	}
}

// TestSanitizedBranchStaysUnderWorktreesDir is the F-14 acceptance test: a
// traversal title, once sanitized and joined, must resolve under the worktrees
// directory rather than escaping it.
func TestSanitizedBranchStaysUnderWorktreesDir(t *testing.T) {
	worktreesDir := t.TempDir()
	for _, title := range []string{"../../x", `..\..\Windows`, "../../", "normal title"} {
		joined := filepath.Join(worktreesDir, sanitizeBranchName(title))
		under, err := IsUnderDir(worktreesDir, joined)
		if err != nil {
			t.Fatalf("IsUnderDir error for title %q: %v", title, err)
		}
		if !under {
			t.Fatalf("title %q -> %q escaped worktrees dir %q", title, joined, worktreesDir)
		}
	}
}
