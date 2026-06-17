//go:build windows

package agentcmd

import "golang.org/x/sys/windows"

// tokenizeProgram splits a command line using Windows CommandLineToArgvW
// semantics (via DecomposeCommandLine). This is intentionally NOT a POSIX
// tokenizer: POSIX rules treat `\` as an escape and would mangle Windows paths
// such as C:\tools\agent.exe. An empty/whitespace command line yields no tokens.
func tokenizeProgram(s string) ([]string, error) {
	if s == "" {
		return nil, nil
	}
	return windows.DecomposeCommandLine(s)
}
