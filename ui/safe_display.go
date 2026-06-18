package ui

import (
	"regexp"
	"strings"
)

var (
	terminalEscapeRe  = regexp.MustCompile(`(?:\x1b\[[0-9;?]*[ -/]*[@-~]|\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)|\x1bP[\s\S]*?(?:\x1b\\))`)
	residualControlRe = regexp.MustCompile(`[\x00-\x08\x0b-\x1f\x7f-\x9f]`)
)

// StripTerminalEscapes removes terminal control sequences from untrusted text.
func StripTerminalEscapes(s string) string {
	s = strings.ToValidUTF8(s, "�")
	return terminalEscapeRe.ReplaceAllString(s, "")
}

// SafeDisplay strips terminal controls while preserving visible user text.
func SafeDisplay(s string) string {
	return residualControlRe.ReplaceAllString(StripTerminalEscapes(s), "")
}
