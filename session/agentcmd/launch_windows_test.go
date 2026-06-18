//go:build windows

package agentcmd

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// On Windows, backslashes are literal path separators (not POSIX escapes), so a
// double-quoted Windows path with spaces is tokenized into a single argv element
// with its backslashes preserved. This exercises tokenize_windows.go.
func TestParseProgramWindowsQuotedPath(t *testing.T) {
	got, err := ParseProgram(`"C:\Program Files\app\agent.exe" --flag`)
	require.NoError(t, err)
	require.Equal(t, []string{`C:\Program Files\app\agent.exe`, "--flag"}, got)
}

func TestBuildLaunchArgvWindowsQuotedPath(t *testing.T) {
	spec, err := BuildLaunch(`"C:\Program Files\app\agent.exe" --flag`, "", "--resume=", ShellNone)
	require.NoError(t, err)
	require.Equal(t, `C:\Program Files\app\agent.exe`, spec.Path)
	require.Equal(t, []string{"--flag"}, spec.Args)
}
