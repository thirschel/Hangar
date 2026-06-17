//go:build !windows

package agentcmd

import "fmt"

// tokenizeProgram splits a command line using POSIX shell word-splitting rules:
// whitespace separates words; single quotes preserve everything literally;
// double quotes preserve everything except `\`-escapes of `"` and `\`; a bare
// `\` escapes the next character. Shell operators (`;`, `&`, `|`) are NOT treated
// as separators here — they remain part of the word, so they are passed as a
// literal argv element and (because callers exec argv directly, never via a
// shell) can never act as a command separator.
func tokenizeProgram(s string) ([]string, error) {
	var (
		tokens  []string
		cur     []rune
		hasTok  bool
		inSingle bool
		inDouble bool
		escaped  bool
	)
	flush := func() {
		if hasTok {
			tokens = append(tokens, string(cur))
			cur = cur[:0]
			hasTok = false
		}
	}
	for _, r := range s {
		switch {
		case escaped:
			cur = append(cur, r)
			hasTok = true
			escaped = false
		case inSingle:
			if r == '\'' {
				inSingle = false
			} else {
				cur = append(cur, r)
			}
		case inDouble:
			switch r {
			case '"':
				inDouble = false
			case '\\':
				escaped = true
			default:
				cur = append(cur, r)
			}
		case r == '\\':
			escaped = true
			hasTok = true
		case r == '\'':
			inSingle = true
			hasTok = true
		case r == '"':
			inDouble = true
			hasTok = true
		case r == ' ' || r == '\t' || r == '\n' || r == '\r':
			flush()
		default:
			cur = append(cur, r)
			hasTok = true
		}
	}
	if inSingle || inDouble {
		return nil, fmt.Errorf("unbalanced quotes")
	}
	if escaped {
		return nil, fmt.Errorf("dangling escape")
	}
	flush()
	return tokens, nil
}
