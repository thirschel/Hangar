//go:build !windows

package agentcmd

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// On POSIX, a double-quoted path that contains spaces is tokenized into a single
// argv element. POSIX agent paths use forward slashes, which round-trip cleanly
// through tokenize_other.go (Windows-style backslash paths are covered in the
// windows-tagged test, since a bare backslash is a POSIX escape character).
func TestParseProgramPosixQuotedPath(t *testing.T) {
	got, err := ParseProgram(`"/opt/app dir/agent" --flag`)
	require.NoError(t, err)
	require.Equal(t, []string{"/opt/app dir/agent", "--flag"}, got)
}

func TestBuildLaunchArgvPosixQuotedPath(t *testing.T) {
	spec, err := BuildLaunch(`"/opt/app dir/agent" --flag`, "", "--resume=", ShellNone)
	require.NoError(t, err)
	require.Equal(t, "/opt/app dir/agent", spec.Path)
	require.Equal(t, []string{"--flag"}, spec.Args)
}
