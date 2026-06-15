package cmd

import (
	"bytes"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

type Executor interface {
	Run(cmd *exec.Cmd) error
	Output(cmd *exec.Cmd) ([]byte, error)
}

type Exec struct{}

func (e Exec) Run(cmd *exec.Cmd) error {
	// Capture stderr (when the caller hasn't already) so a failure carries the
	// command's real message instead of a bare "exit status 1".
	var stderr *bytes.Buffer
	if cmd.Stderr == nil {
		stderr = &bytes.Buffer{}
		cmd.Stderr = stderr
	}
	err := cmd.Run()
	if err != nil && stderr != nil {
		return appendStderr(err, stderr.Bytes())
	}
	return err
}

func (e Exec) Output(cmd *exec.Cmd) ([]byte, error) {
	out, err := cmd.Output()
	if err != nil {
		// cmd.Output populates ExitError.Stderr when cmd.Stderr was nil.
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return out, appendStderr(err, exitErr.Stderr)
		}
	}
	return out, err
}

// appendStderr wraps err with the trimmed stderr output, if any, so callers see
// the real reason a command failed. The original error is wrapped with %w so
// errors.As/errors.Is continue to work.
func appendStderr(err error, stderr []byte) error {
	msg := strings.TrimSpace(string(stderr))
	if msg == "" {
		return err
	}
	return fmt.Errorf("%w: %s", err, msg)
}

func MakeExecutor() Executor {
	return Exec{}
}

func ToString(cmd *exec.Cmd) string {
	if cmd == nil {
		return "<nil>"
	}
	return strings.Join(cmd.Args, " ")
}
