// Package copilot discovers GitHub Copilot CLI session-state directories on disk.
package copilot

import (
	"os"
	"path/filepath"
	"runtime"
)

// Root resolves the local GitHub Copilot CLI session-state root directory.
func Root() string {
	if override := os.Getenv("CS_COPILOT_SESSION_DIR"); override != "" {
		return override
	}

	home := ""
	if runtime.GOOS == "windows" {
		home = os.Getenv("USERPROFILE")
	}
	if home == "" {
		if dir, err := os.UserHomeDir(); err == nil {
			home = dir
		}
	}

	return filepath.Join(home, ".copilot", "session-state")
}
