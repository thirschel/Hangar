//go:build windows

package app

import (
	"io"

	"claude-squad/session"

	tea "github.com/charmbracelet/bubbletea"
)

// startAttach (native Windows) shows the attach help overlay and queues the
// attach. The actual attach runs via tea.Exec (see consumePendingAttach) so
// bubbletea releases the terminal (stopping its own input reader and renderer)
// while the agent owns the console, then fully restores + repaints on detach.
// This is what prevents stolen keystrokes and the overlapping-screen corruption
// that a plain blocking attach causes on Windows.
func (m *home) startAttach(selected *session.Instance) (tea.Model, tea.Cmd) {
	m.pendingAttach = selected
	model, cmd := m.showHelpScreen(helpTypeInstanceAttach{}, func() {})
	if m.state == stateHelp {
		// Help overlay is showing; the attach fires on dismiss via
		// consumePendingAttach (called from handleHelpState).
		return model, cmd
	}
	// Help was skipped (already seen) — attach immediately.
	return m, m.consumePendingAttach()
}

// consumePendingAttach returns a tea.Exec command that attaches to the queued
// instance, or nil if none is queued.
func (m *home) consumePendingAttach() tea.Cmd {
	selected := m.pendingAttach
	if selected == nil {
		return nil
	}
	m.pendingAttach = nil
	return tea.Exec(&attachExec{instance: selected}, func(err error) tea.Msg {
		return attachFinishedMsg{err: err}
	})
}

// attachExec adapts an instance attach to bubbletea's ExecCommand so it runs
// with the terminal released. It blocks until the user detaches (Ctrl-Q) or the
// agent exits.
type attachExec struct {
	instance *session.Instance
}

func (a *attachExec) SetStdin(io.Reader)  {}
func (a *attachExec) SetStdout(io.Writer) {}
func (a *attachExec) SetStderr(io.Writer) {}

func (a *attachExec) Run() error {
	ch, err := a.instance.Attach()
	if err != nil {
		return err
	}
	<-ch
	return nil
}
