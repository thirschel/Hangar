package tmux

import (
	cmd2 "claude-squad/cmd"
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"claude-squad/cmd/cmd_test"

	"github.com/stretchr/testify/require"
)

type MockPtyFactory struct {
	t *testing.T

	// Array of commands and the corresponding file handles representing PTYs.
	cmds  []*exec.Cmd
	files []*os.File
}

func (pt *MockPtyFactory) Start(cmd *exec.Cmd) (*os.File, error) {
	filePath := filepath.Join(pt.t.TempDir(), fmt.Sprintf("pty-%s-%d", pt.t.Name(), rand.Int31()))
	f, err := os.OpenFile(filePath, os.O_CREATE|os.O_RDWR, 0644)
	if err == nil {
		pt.cmds = append(pt.cmds, cmd)
		pt.files = append(pt.files, f)
	}
	return f, err
}

func (pt *MockPtyFactory) Close() {}

func NewMockPtyFactory(t *testing.T) *MockPtyFactory {
	return &MockPtyFactory{
		t: t,
	}
}

func TestSanitizeName(t *testing.T) {
	session := NewTmuxSession("asdf", "program")
	require.Equal(t, TmuxPrefix+"asdf", session.sanitizedName)

	session = NewTmuxSession("a sd f . . asdf", "program")
	require.Equal(t, TmuxPrefix+"asdf__asdf", session.sanitizedName)
}

func TestStartTmuxSession(t *testing.T) {
	ptyFactory := NewMockPtyFactory(t)

	created := false
	cmdExec := cmd_test.MockCmdExec{
		RunFunc: func(cmd *exec.Cmd) error {
			if strings.Contains(cmd.String(), "has-session") && !created {
				created = true
				return fmt.Errorf("session already exists")
			}
			return nil
		},
		OutputFunc: func(cmd *exec.Cmd) ([]byte, error) {
			return []byte("output"), nil
		},
	}

	workdir := t.TempDir()
	session := newTmuxSession("test-session", "claude", ptyFactory, cmdExec)

	err := session.Start(workdir)
	require.NoError(t, err)
	require.Equal(t, 2, len(ptyFactory.cmds))
	require.Equal(t, fmt.Sprintf("tmux new-session -d -s claudesquad_test-session -c %s claude", workdir),
		cmd2.ToString(ptyFactory.cmds[0]))
	require.Equal(t, "tmux attach-session -t claudesquad_test-session",
		cmd2.ToString(ptyFactory.cmds[1]))

	require.Equal(t, 2, len(ptyFactory.files))

	// File should be closed.
	_, err = ptyFactory.files[0].Stat()
	require.Error(t, err)
	// File should be open
	_, err = ptyFactory.files[1].Stat()
	require.NoError(t, err)
}

func TestDiagnoseProgramNotOnPath(t *testing.T) {
	lp := func(string) (string, error) { return "", fmt.Errorf("not found") }
	err := diagnoseProgram("copilot", lp, "linux", func() string { return "" }, "/tmp/claudesquad.log")
	require.Error(t, err)
	require.Contains(t, err.Error(), "not found on PATH")
}

func TestDiagnoseProgramWindowsShim(t *testing.T) {
	lp := func(string) (string, error) {
		return "/mnt/c/Users/x/AppData/Roaming/npm/copilot", nil
	}
	err := diagnoseProgram("copilot", lp, "linux", func() string { return "" }, "")
	require.Error(t, err)
	require.Contains(t, err.Error(), "Windows executable")
}

func TestDiagnoseProgramSurfacesProbeOutput(t *testing.T) {
	lp := func(string) (string, error) { return "/usr/local/bin/copilot", nil }
	probe := func() string {
		return "copilot: /lib/x86_64-linux-gnu/libc.so.6: version `GLIBC_2.28' not found (required by copilot)"
	}
	err := diagnoseProgram("copilot", lp, "linux", probe, "")
	require.Error(t, err)
	require.Contains(t, err.Error(), "GLIBC_2.28")
}

func TestDiagnoseProgramGenericHint(t *testing.T) {
	lp := func(string) (string, error) { return "/usr/local/bin/copilot", nil }
	err := diagnoseProgram("copilot", lp, "linux", func() string { return "" }, "/tmp/claudesquad.log")
	require.Error(t, err)
	require.Contains(t, err.Error(), "verify it runs standalone")
	require.Contains(t, err.Error(), "/tmp/claudesquad.log")
}

func TestDiagnoseProgramEmpty(t *testing.T) {
	err := diagnoseProgram("   ", func(string) (string, error) { return "", nil }, "linux", nil, "")
	require.Error(t, err)
	require.Contains(t, err.Error(), "no program")
}

func TestAnnotateCaptureErrorAddsHintOnDeadPane(t *testing.T) {
	err := annotateCaptureError("error capturing pane content",
		fmt.Errorf("exit status 1: can't find pane: claudesquad_x"))
	require.Contains(t, err.Error(), "agent process appears to have exited")
}

func TestAnnotateCaptureErrorPlainOtherwise(t *testing.T) {
	err := annotateCaptureError("error capturing pane content", fmt.Errorf("exit status 1: some other error"))
	require.Contains(t, err.Error(), "error capturing pane content")
	require.NotContains(t, err.Error(), "agent process appears to have exited")
}
