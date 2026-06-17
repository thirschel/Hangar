package agentcmd

import (
	"path/filepath"
	"strings"
)

// SupportsResume reports whether the agent launched by program supports a stable,
// resumable Copilot-style session id.
func SupportsResume(program string) bool {
	fields := strings.Fields(program)
	if len(fields) == 0 {
		return false
	}

	name := filepath.Base(fields[0])
	if runtimeBase := filepath.Base(strings.ReplaceAll(fields[0], `\`, string(filepath.Separator))); runtimeBase != name {
		name = runtimeBase
	}

	lowerName := strings.ToLower(name)
	if strings.HasSuffix(lowerName, ".exe") {
		lowerName = strings.TrimSuffix(lowerName, ".exe")
	} else if strings.HasSuffix(lowerName, ".cmd") {
		lowerName = strings.TrimSuffix(lowerName, ".cmd")
	}

	return lowerName == "copilot"
}

// SeedNewCommand returns the command to launch a new session seeded with id.
func SeedNewCommand(program, id string) string {
	if id == "" || !SupportsResume(program) {
		return program
	}

	return program + " --session-id=" + id
}

// ResumeCommand returns the command to resume an existing session id.
func ResumeCommand(program, id string) string {
	if id == "" || !SupportsResume(program) {
		return program
	}

	return program + " --resume=" + id
}
