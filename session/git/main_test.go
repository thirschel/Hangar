package git

import (
	"os"
	"testing"

	cslog "hangar/log"
)

// TestMain initializes the global logger so tests that drive worktree creation —
// which now calls config.LoadConfig() to resolve the worktree directory — don't
// nil-panic on log.ErrorLog when a default config has to be synthesized.
func TestMain(m *testing.M) {
	cslog.Initialize(false)
	code := m.Run()
	cslog.Close()
	os.Exit(code)
}
