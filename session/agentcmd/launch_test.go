package agentcmd

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestValidSessionID(t *testing.T) {
	tests := []struct {
		name string
		id   string
		want bool
	}{
		{name: "real uuid", id: "123e4567-e89b-12d3-a456-426614174000", want: true},
		{name: "short hex", id: "deadbeef", want: true},
		{name: "hex with dashes", id: "abcdef12-3456", want: true},
		{name: "too short", id: "abc", want: false},
		{name: "empty", id: "", want: false},
		{name: "too long", id: repeat("a", 65), want: false},
		{name: "ampersand injection", id: "a&calc.exe", want: false},
		{name: "semicolon injection", id: "a;calc", want: false},
		{name: "pipe injection", id: "a|calc", want: false},
		{name: "path traversal", id: "../x", want: false},
		{name: "space", id: "aa aa aa", want: false},
		{name: "non-hex letters", id: "origin-freeze", want: false},
		{name: "quote", id: "a'); calc; ('", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, ValidSessionID(tt.id))
		})
	}
}

func TestParseProgram(t *testing.T) {
	tests := []struct {
		name    string
		program string
		want    []string
		wantErr bool
	}{
		{name: "simple", program: "copilot", want: []string{"copilot"}},
		{name: "multi token", program: "aider --model x", want: []string{"aider", "--model", "x"}},
		{name: "profile value", program: "aider --model ollama_chat/gemma3:1b", want: []string{"aider", "--model", "ollama_chat/gemma3:1b"}},
		{name: "quoted profile", program: `cs -p "codex"`, want: []string{"cs", "-p", "codex"}},
		{name: "empty", program: "", wantErr: true},
		{name: "whitespace only", program: "   ", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseProgram(tt.program)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestParseShellKind(t *testing.T) {
	require.Equal(t, ShellNone, ParseShellKind(""))
	require.Equal(t, ShellNone, ParseShellKind("cmd"))
	require.Equal(t, ShellNone, ParseShellKind("CMD"))
	require.Equal(t, ShellPowerShell, ParseShellKind("powershell"))
	require.Equal(t, ShellPwsh, ParseShellKind("pwsh"))
}

// TestBuildLaunchInjectionStaysSingleArg proves that even an un-validated,
// metacharacter-laden resume id becomes exactly ONE argv element and is never
// split into a shell separator. (The id is also independently rejected by
// ValidSessionID at the trust boundary.)
func TestBuildLaunchInjectionStaysSingleArg(t *testing.T) {
	for _, id := range []string{"a&calc.exe", "a;calc", "a|calc", "a&&calc"} {
		t.Run(id, func(t *testing.T) {
			require.False(t, ValidSessionID(id), "injection id must be rejected by the validator")

			spec, err := ResumeLaunch("copilot", id, ShellNone)
			require.NoError(t, err)
			require.Equal(t, "copilot", spec.Path)
			require.Equal(t, []string{"--resume=" + id}, spec.Args,
				"resume id must be exactly one argv element, never split")
		})
	}
}

func TestBuildLaunchArgvMode(t *testing.T) {
	t.Run("aider multi-arg no resume", func(t *testing.T) {
		spec, err := BuildLaunch("aider --model x", "", "--resume=", ShellNone)
		require.NoError(t, err)
		require.Equal(t, ShellNone, spec.Shell)
		require.Equal(t, "aider", spec.Path)
		require.Equal(t, []string{"--model", "x"}, spec.Args)
	})

	t.Run("copilot resume valid uuid", func(t *testing.T) {
		id := "123e4567-e89b-12d3-a456-426614174000"
		spec, err := ResumeLaunch("copilot", id, ShellNone)
		require.NoError(t, err)
		require.Equal(t, "copilot", spec.Path)
		require.Equal(t, []string{"--resume=" + id}, spec.Args)
	})

	t.Run("seed new uuid", func(t *testing.T) {
		id := "123e4567-e89b-12d3-a456-426614174000"
		spec, err := SeedNewLaunch("copilot", id, ShellNone)
		require.NoError(t, err)
		require.Equal(t, "copilot", spec.Path)
		require.Equal(t, []string{"--session-id=" + id}, spec.Args)
	})

	t.Run("resume ignored for unsupported agent", func(t *testing.T) {
		spec, err := ResumeLaunch("claude", "123e4567-e89b-12d3-a456-426614174000", ShellNone)
		require.NoError(t, err)
		require.Equal(t, "claude", spec.Path)
		require.Empty(t, spec.Args)
	})
}

func TestBuildLaunchShellMode(t *testing.T) {
	t.Run("cpa powershell function no resume", func(t *testing.T) {
		spec, err := BuildLaunch("cpa", "", "--resume=", ShellPowerShell)
		require.NoError(t, err)
		require.Equal(t, ShellPowerShell, spec.Shell)
		require.Equal(t, "cpa", spec.Script)
		require.Empty(t, spec.Env)
	})

	t.Run("copilot resume id env-bound, not interpolated", func(t *testing.T) {
		id := "123e4567-e89b-12d3-a456-426614174000"
		spec, err := ResumeLaunch("copilot", id, ShellPowerShell)
		require.NoError(t, err)
		require.Equal(t, ShellPowerShell, spec.Shell)
		require.Equal(t, "copilot --resume=$env:HANGAR_AGENT_RESUME", spec.Script)
		require.Equal(t, []string{"HANGAR_AGENT_RESUME=" + id}, spec.Env)
		// The literal id must NOT appear in the script text.
		require.NotContains(t, spec.Script, id)
	})

	t.Run("empty program errors", func(t *testing.T) {
		_, err := BuildLaunch("", "", "--resume=", ShellPowerShell)
		require.Error(t, err)
	})
}

func repeat(s string, n int) string {
	out := make([]byte, 0, len(s)*n)
	for i := 0; i < n; i++ {
		out = append(out, s...)
	}
	return string(out)
}
