package agentcmd

import (
	"fmt"
	"regexp"
	"strings"
)

// sessionIDRe accepts UUID-ish ids: hex digits plus dashes, 8-64 chars. By
// construction it rejects every shell metacharacter (& ; | ^ ( ) space ' " etc.)
// and path traversal (`.`, `/`, `\`), so a value that passes can never act as a
// command separator or break out of a launch argument.
var sessionIDRe = regexp.MustCompile(`^[0-9A-Fa-f-]{8,64}$`)

// ValidSessionID reports whether s is a safe Copilot/agent session id. It is the
// trust-boundary gate for F-01: untrusted ids (from workspace.yaml, persisted
// state, or IPC requests) must pass this before they are reused on launch.
func ValidSessionID(s string) bool {
	return sessionIDRe.MatchString(s)
}

// ShellKind selects how the agent is launched.
type ShellKind int

const (
	// ShellNone launches the program directly via argv (exec.Command(path, args...))
	// with no shell interpreter. This is the default, safe path.
	ShellNone ShellKind = iota
	// ShellPowerShell launches via `powershell.exe -Command` (explicit opt-in,
	// needed for PowerShell-function agents such as `cpa`).
	ShellPowerShell
	// ShellPwsh launches via `pwsh.exe -Command` (explicit opt-in).
	ShellPwsh
)

// ParseShellKind maps the persisted/config shell string to a ShellKind.
// "" and "cmd" map to the safe argv launch; only an explicit "powershell"/"pwsh"
// opts into a shell interpreter.
func ParseShellKind(s string) ShellKind {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "powershell":
		return ShellPowerShell
	case "pwsh":
		return ShellPwsh
	default:
		return ShellNone
	}
}

// LaunchSpec is the structured, shell-free description of how to start an agent.
// It is built ONCE (the program string is tokenized a single time) and carried
// to the platform launcher, which never re-splits it.
type LaunchSpec struct {
	Shell ShellKind

	// ShellNone (argv mode):
	Path string   // argv[0]
	Args []string // argv[1:], including any --resume=<id> as a discrete element

	// ShellPowerShell/ShellPwsh (opt-in shell mode):
	Script string   // trusted command text passed to -Command
	Env    []string // extra environment ("HANGAR_AGENT_RESUME=<id>"), binds the resume id
}

// ParseProgram splits a configured program string into argv exactly once, using
// platform-correct tokenization (CommandLineToArgvW semantics on Windows so that
// backslash paths like C:\tools\agent.exe survive; POSIX rules on Unix).
func ParseProgram(program string) ([]string, error) {
	toks, err := tokenizeProgram(strings.TrimSpace(program))
	if err != nil {
		return nil, fmt.Errorf("invalid program %q: %w", program, err)
	}
	if len(toks) == 0 {
		return nil, fmt.Errorf("empty program %q", program)
	}
	return toks, nil
}

// resumeFlag returns the discrete argv element for a resumable agent, or "".
// flag is "--resume=" or "--session-id=". The id is one token; it is never
// re-split or shell-interpreted.
func resumeFlag(program, id, flag string) string {
	if id == "" || !SupportsResume(program) {
		return ""
	}
	return flag + id
}

// BuildLaunch produces the structured launch spec. resumeID may be "".
//
// In ShellNone mode the program is tokenized to argv and the resume flag is
// appended as its own element. In shell mode the resume id is bound to the
// HANGAR_AGENT_RESUME environment variable and referenced (never re-tokenized)
// from the script, so even an unexpected id cannot inject.
func BuildLaunch(program, resumeID, flag string, shell ShellKind) (LaunchSpec, error) {
	switch shell {
	case ShellNone:
		argv, err := ParseProgram(program)
		if err != nil {
			return LaunchSpec{}, err
		}
		if f := resumeFlag(program, resumeID, flag); f != "" {
			argv = append(argv, f)
		}
		return LaunchSpec{Shell: ShellNone, Path: argv[0], Args: argv[1:]}, nil
	case ShellPowerShell, ShellPwsh:
		script := strings.TrimSpace(program)
		if script == "" {
			return LaunchSpec{}, fmt.Errorf("empty program")
		}
		var env []string
		if f := resumeFlag(program, resumeID, flag); f != "" {
			// $env:HANGAR_AGENT_RESUME expands to a single argument token in
			// PowerShell; its contents are NOT re-tokenized for ; & | etc.
			flagName := strings.TrimSuffix(flag, "=")
			script = script + " " + flagName + "=$env:HANGAR_AGENT_RESUME"
			env = []string{"HANGAR_AGENT_RESUME=" + resumeID}
		}
		return LaunchSpec{Shell: shell, Script: script, Env: env}, nil
	}
	return LaunchSpec{}, fmt.Errorf("unknown shell kind")
}

// ResumeLaunch builds a LaunchSpec that resumes an existing session id.
func ResumeLaunch(program, id string, shell ShellKind) (LaunchSpec, error) {
	return BuildLaunch(program, id, "--resume=", shell)
}

// SeedNewLaunch builds a LaunchSpec that seeds a new session id.
func SeedNewLaunch(program, id string, shell ShellKind) (LaunchSpec, error) {
	return BuildLaunch(program, id, "--session-id=", shell)
}
