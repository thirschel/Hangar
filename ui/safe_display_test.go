package ui

import "testing"

func TestSafeDisplayStripsTerminalEscapes(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"\x1b]0;PWNED\x07hello":                "hello",
		"\x1b]52;c;Zm9v\x07copy me":            "copy me",
		"\x1b[31mcolored\x1b[0m text":          "colored text",
		"\x1bP$qm\x1b\\repo":                   "repo",
		"keep\ttext\x01and strip controls\x7f": "keep\ttextand strip controls",
	}

	for in, want := range cases {
		if got := SafeDisplay(in); got != want {
			t.Fatalf("SafeDisplay(%q) = %q, want %q", in, got, want)
		}
	}
}
