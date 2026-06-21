package ui

import tea "github.com/charmbracelet/bubbletea"

// KeyMsgToBytes translates a Bubble Tea key message into the raw byte sequence
// that a real terminal/PTY would emit for that key. It is intended for raw
// passthrough of keystrokes to an agent's PTY (for example via SendKeys), so it
// favors terminal fidelity over Bubble Tea's friendly key names. Unhandled key
// types return nil.
func KeyMsgToBytes(msg tea.KeyMsg) []byte {
	switch msg.Type {
	case tea.KeyRunes, tea.KeySpace:
		// Printable input. KeySpace always carries Runes == []rune{' '} in
		// bubbletea, so string(msg.Runes) yields the correct bytes for both
		// cases. Pasted text is delivered in msg.Runes as well and is forwarded
		// verbatim.
		out := []byte(string(msg.Runes))
		if msg.Alt {
			// The Alt/Meta modifier is sent as an ESC prefix before the runes.
			out = append([]byte{0x1b}, out...)
		}
		return out
	case tea.KeyEnter:
		return []byte{'\r'}
	case tea.KeyTab:
		return []byte{'\t'}
	case tea.KeyShiftTab:
		return []byte("\x1b[Z")
	case tea.KeyBackspace:
		return []byte{0x7f}
	case tea.KeyEsc:
		return []byte{0x1b}
	case tea.KeyUp:
		return []byte("\x1b[A")
	case tea.KeyDown:
		return []byte("\x1b[B")
	case tea.KeyRight:
		return []byte("\x1b[C")
	case tea.KeyLeft:
		return []byte("\x1b[D")
	case tea.KeyHome:
		return []byte("\x1b[H")
	case tea.KeyEnd:
		return []byte("\x1b[F")
	case tea.KeyPgUp:
		return []byte("\x1b[5~")
	case tea.KeyPgDown:
		return []byte("\x1b[6~")
	case tea.KeyDelete:
		return []byte("\x1b[3~")
	case tea.KeyInsert:
		return []byte("\x1b[2~")
	}

	// Control keys. bubbletea defines KeyCtrlAt (Ctrl-@) through
	// KeyCtrlUnderscore as their literal C0 control-byte values: Ctrl-@ == 0x00,
	// Ctrl-A == 0x01 ... Ctrl-Z == 0x1a, and Ctrl-\ == 0x1c ... Ctrl-_ == 0x1f.
	// Emitting byte(msg.Type) therefore reproduces the exact control code.
	//
	// Tab (0x09), Enter (0x0d) and Esc (0x1b) share constant values with
	// KeyCtrlI, KeyCtrlM and KeyCtrlOpenBracket respectively; they are handled
	// by the cases above and so cannot be repeated here.
	if msg.Type >= tea.KeyCtrlAt && msg.Type <= tea.KeyCtrlUnderscore {
		return []byte{byte(msg.Type)}
	}

	return nil
}
