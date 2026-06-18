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

// SeedFlagCommand appends copilot's --session-id flag for a validated id WITHOUT
// gating on the program name. Resumability is decided by the caller (e.g. an
// auto-detected copilot wrapper such as `cpa`, whose name is not "copilot"); the
// id is still charset-validated so it can never break out of the launch argument.
// An empty or invalid id leaves the program unchanged.
func SeedFlagCommand(program, id string) string {
	if id == "" || !ValidSessionID(id) {
		return program
	}

	return program + " --session-id=" + id
}

// ResumeFlagCommand appends copilot's --resume flag for a validated id WITHOUT
// gating on the program name (see SeedFlagCommand). An empty or invalid id leaves
// the program unchanged.
func ResumeFlagCommand(program, id string) string {
	if id == "" || !ValidSessionID(id) {
		return program
	}

	return program + " --resume=" + id
}
