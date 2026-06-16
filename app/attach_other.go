//go:build !windows

package app

import (
	"claude-squad/session"

	tea "github.com/charmbracelet/bubbletea"
)

// startAttach (Unix) preserves the existing tmux behaviour: show the attach help
// overlay, then attach by blocking on the session's detach channel inside the
// overlay-dismiss callback. The tmux PTY shares the terminal raw mode bubbletea
// has already set up.
func (m *home) startAttach(selected *session.Instance) (tea.Model, tea.Cmd) {
	return m.showHelpScreen(helpTypeInstanceAttach{}, func() {
		ch, err := m.list.Attach()
		if err != nil {
			m.handleError(err)
			return
		}
		<-ch
		m.state = stateDefault
		m.instanceChanged()
	})
}

// consumePendingAttach is a no-op on Unix (the tea.Exec attach path is
// Windows-only).
func (m *home) consumePendingAttach() tea.Cmd { return nil }
