package agentcmd

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestResumeCommand(t *testing.T) {
	tests := []struct {
		name    string
		program string
		id      string
		want    string
	}{
		{
			name:    "copilot",
			program: "copilot",
			id:      "abc",
			want:    "copilot --resume=abc",
		},
		{
			name:    "extra args",
			program: "copilot --banner",
			id:      "abc",
			want:    "copilot --banner --resume=abc",
		},
		{
			name:    "empty id",
			program: "copilot",
			id:      "",
			want:    "copilot",
		},
		{
			name:    "unsupported agent",
			program: "claude",
			id:      "abc",
			want:    "claude",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, ResumeCommand(tt.program, tt.id))
		})
	}
}

func TestSeedNewCommand(t *testing.T) {
	tests := []struct {
		name    string
		program string
		id      string
		want    string
	}{
		{
			name:    "copilot",
			program: "copilot",
			id:      "abc",
			want:    "copilot --session-id=abc",
		},
		{
			name:    "empty id",
			program: "copilot",
			id:      "",
			want:    "copilot",
		},
		{
			name:    "unsupported agent",
			program: "claude",
			id:      "abc",
			want:    "claude",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, SeedNewCommand(tt.program, tt.id))
		})
	}
}

func TestSeedFlagCommand(t *testing.T) {
	const id = "0cb916db-26aa-40f2-86b5-1ba81b225fd2"
	// A wrapper name (cpa) is NOT gated: a valid id is appended regardless of name,
	// so an auto-detected copilot wrapper still gets a seeded session.
	require.Equal(t, "cpa --session-id="+id, SeedFlagCommand("cpa", id))
	require.Equal(t, "cpa --allow-all --session-id="+id, SeedFlagCommand("cpa --allow-all", id))
	require.Equal(t, "copilot --session-id="+id, SeedFlagCommand("copilot", id))
	// Empty or invalid ids leave the program unchanged (trust-boundary gate).
	require.Equal(t, "cpa", SeedFlagCommand("cpa", ""))
	require.Equal(t, "cpa", SeedFlagCommand("cpa", "bad;id"))
	require.Equal(t, "cpa", SeedFlagCommand("cpa", "short"))
}

func TestResumeFlagCommand(t *testing.T) {
	const id = "0cb916db-26aa-40f2-86b5-1ba81b225fd2"
	require.Equal(t, "cpa --resume="+id, ResumeFlagCommand("cpa", id))
	require.Equal(t, "cpa --allow-all --resume="+id, ResumeFlagCommand("cpa --allow-all", id))
	require.Equal(t, "copilot --resume="+id, ResumeFlagCommand("copilot", id))
	require.Equal(t, "cpa", ResumeFlagCommand("cpa", ""))
	require.Equal(t, "cpa", ResumeFlagCommand("cpa", "bad;id"))
}

func TestSupportsResume(t *testing.T) {
	tests := []struct {
		name    string
		program string
		want    bool
	}{
		{name: "copilot", program: "copilot", want: true},
		{name: "copilot with args", program: "copilot --banner", want: true},
		{name: "unix path", program: "/usr/bin/copilot", want: true},
		{name: "windows exe path", program: `C:\tools\copilot.exe`, want: true},
		{name: "cmd extension", program: "copilot.cmd", want: true},
		{name: "claude", program: "claude", want: false},
		{name: "aider with args", program: "aider --model x", want: false},
		{name: "empty", program: "", want: false},
		{name: "copilot not executable", program: "echo copilot is great", want: false},
		{name: "path prompt", program: "say copilot", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, SupportsResume(tt.program))
		})
	}
}
