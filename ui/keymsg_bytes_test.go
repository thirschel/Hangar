package ui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"
)

func TestKeyMsgToBytes(t *testing.T) {
	tests := []struct {
		name string
		msg  tea.KeyMsg
		want []byte
	}{
		{"lowercase rune", tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}}, []byte("a")},
		{"uppercase rune", tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'A'}}, []byte("A")},
		// '✓' (U+2713) encodes to three UTF-8 bytes: 0xE2 0x9C 0x93.
		{"multi-byte rune", tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'✓'}}, []byte{0xe2, 0x9c, 0x93}},
		{"space", tea.KeyMsg{Type: tea.KeySpace, Runes: []rune{' '}}, []byte{' '}},
		{"enter", tea.KeyMsg{Type: tea.KeyEnter}, []byte{'\r'}},
		{"tab", tea.KeyMsg{Type: tea.KeyTab}, []byte{'\t'}},
		{"shift+tab", tea.KeyMsg{Type: tea.KeyShiftTab}, []byte("\x1b[Z")},
		{"backspace", tea.KeyMsg{Type: tea.KeyBackspace}, []byte{0x7f}},
		{"esc", tea.KeyMsg{Type: tea.KeyEsc}, []byte{0x1b}},
		{"up arrow", tea.KeyMsg{Type: tea.KeyUp}, []byte("\x1b[A")},
		{"down arrow", tea.KeyMsg{Type: tea.KeyDown}, []byte("\x1b[B")},
		{"right arrow", tea.KeyMsg{Type: tea.KeyRight}, []byte("\x1b[C")},
		{"left arrow", tea.KeyMsg{Type: tea.KeyLeft}, []byte("\x1b[D")},
		{"home", tea.KeyMsg{Type: tea.KeyHome}, []byte("\x1b[H")},
		{"end", tea.KeyMsg{Type: tea.KeyEnd}, []byte("\x1b[F")},
		{"page up", tea.KeyMsg{Type: tea.KeyPgUp}, []byte("\x1b[5~")},
		{"page down", tea.KeyMsg{Type: tea.KeyPgDown}, []byte("\x1b[6~")},
		{"delete", tea.KeyMsg{Type: tea.KeyDelete}, []byte("\x1b[3~")},
		{"insert", tea.KeyMsg{Type: tea.KeyInsert}, []byte("\x1b[2~")},
		{"ctrl+c", tea.KeyMsg{Type: tea.KeyCtrlC}, []byte{0x03}},
		{"ctrl+a", tea.KeyMsg{Type: tea.KeyCtrlA}, []byte{0x01}},
		{"ctrl+z", tea.KeyMsg{Type: tea.KeyCtrlZ}, []byte{0x1a}},
		{"ctrl+@", tea.KeyMsg{Type: tea.KeyCtrlAt}, []byte{0x00}},
		{"ctrl+backslash", tea.KeyMsg{Type: tea.KeyCtrlBackslash}, []byte{0x1c}},
		{"ctrl+underscore", tea.KeyMsg{Type: tea.KeyCtrlUnderscore}, []byte{0x1f}},
		{"alt+rune prefixes esc", tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'b'}, Alt: true}, []byte{0x1b, 'b'}},
		{"alt+space prefixes esc", tea.KeyMsg{Type: tea.KeySpace, Runes: []rune{' '}, Alt: true}, []byte{0x1b, ' '}},
		{"unhandled key returns nil", tea.KeyMsg{Type: tea.KeyF1}, nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, KeyMsgToBytes(tt.msg))
		})
	}
}
