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
