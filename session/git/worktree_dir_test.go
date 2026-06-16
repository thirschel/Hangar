package git

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestGetWorktreeDirectory_Override verifies that the configurable worktree
// location is honored when set and falls back to <configDir>/worktrees otherwise.
func TestGetWorktreeDirectory_Override(t *testing.T) {
	tempHome := t.TempDir()
	// os.UserHomeDir reads USERPROFILE on Windows and HOME on Unix — set both.
	t.Setenv("HOME", tempHome)
	t.Setenv("USERPROFILE", tempHome)

	csDir := filepath.Join(tempHome, ".claude-squad")
	if err := os.MkdirAll(csDir, 0755); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	cfgPath := filepath.Join(csDir, "config.json")

	writeConfig := func(m map[string]string) {
		data, err := json.Marshal(m)
		if err != nil {
			t.Fatalf("marshal config: %v", err)
		}
		if err := os.WriteFile(cfgPath, data, 0644); err != nil {
			t.Fatalf("write config: %v", err)
		}
	}

	// No override → default location.
	writeConfig(map[string]string{"default_program": "copilot"})
	got, err := getWorktreeDirectory()
	if err != nil {
		t.Fatalf("getWorktreeDirectory: %v", err)
	}
	if want := filepath.Join(csDir, "worktrees"); got != want {
		t.Errorf("default worktree dir = %q, want %q", got, want)
	}

	// Override → used directly.
	custom := filepath.Join(t.TempDir(), "my-workspaces")
	writeConfig(map[string]string{"default_program": "copilot", "worktree_dir": custom})
	got, err = getWorktreeDirectory()
	if err != nil {
		t.Fatalf("getWorktreeDirectory: %v", err)
	}
	if got != custom {
		t.Errorf("override worktree dir = %q, want %q", got, custom)
	}
}
