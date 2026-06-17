package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"claude-squad/session/copilot"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/require"
)

func testBrowserSessions(now time.Time) []copilot.Session {
	return []copilot.Session{
		{ID: "old", Name: "Legacy Fix", Repository: "repo-one", Branch: "main", OriginRef: "refs/heads/main", UpdatedAt: now.Add(-3 * time.Hour), HasEvents: true},
		{ID: "new", Name: "Feature Search", Repository: "repo-two", Branch: "feature/browser", OriginRef: "refs/heads/feature/browser", UpdatedAt: now.Add(-5 * time.Minute), HasEvents: true},
		{ID: "mid", Name: "Bug Bash", Repository: "repo-one", Branch: "bug/windows", OriginRef: "refs/heads/bug/windows", UpdatedAt: now.Add(-1 * time.Hour), HasEvents: false},
	}
}

func TestSessionBrowserSetSessionsSetQueryFilters(t *testing.T) {
	b := NewSessionBrowser()
	b.SetSessions(testBrowserSessions(time.Now()))

	b.SetQuery("REPO-one bug")
	require.Len(t, b.filtered, 1)
	require.Equal(t, "mid", b.filtered[0].ID)

	b.SetQuery("feature browser")
	require.Len(t, b.filtered, 1)
	require.Equal(t, "new", b.filtered[0].ID)

	b.SetQuery("does-not-match")
	require.Empty(t, b.filtered)
	require.Nil(t, b.GetSelected())
}

func TestSessionBrowserSelectionClampAndWrap(t *testing.T) {
	b := NewSessionBrowser()
	b.SetSessions(testBrowserSessions(time.Now()))
	require.Len(t, b.filtered, 3)

	b.HandleKeyPress(tea.KeyMsg{Type: tea.KeyUp})
	require.Equal(t, 2, b.selectedIdx)

	b.HandleKeyPress(tea.KeyMsg{Type: tea.KeyDown})
	require.Equal(t, 0, b.selectedIdx)

	b.selectedIdx = 2
	b.SetQuery("feature")
	require.Len(t, b.filtered, 1)
	require.Equal(t, 0, b.selectedIdx)
	require.Equal(t, "new", b.GetSelected().ID)

	b.HandleKeyPress(tea.KeyMsg{Type: tea.KeyDown})
	require.Equal(t, 0, b.selectedIdx)
}

func TestSessionBrowserDefaultOrderUpdatedAtDesc(t *testing.T) {
	b := NewSessionBrowser()
	b.SetSessions(testBrowserSessions(time.Now()))

	require.Equal(t, []string{"new", "mid", "old"}, []string{b.filtered[0].ID, b.filtered[1].ID, b.filtered[2].ID})
}

func TestSessionBrowserStringEmptyStates(t *testing.T) {
	b := NewSessionBrowser()
	b.SetSize(80, 20)
	require.Contains(t, b.String(), "No Copilot sessions found")

	b.SetSessions(testBrowserSessions(time.Now()))
	b.SetQuery("zzz")
	require.Contains(t, b.String(), "No matches for \"zzz\"")
}

func TestSessionBrowserStringZeroSizeAndMissingEventsPreview(t *testing.T) {
	b := NewSessionBrowser()
	b.SetSessions([]copilot.Session{{
		ID:         "missing-events",
		Dir:        "C:\\definitely-does-not-exist\\claude-squad-browser-test",
		Name:       "Missing Events",
		Repository: "repo",
		Branch:     "main",
		OriginRoot: "C:\\repo",
		UpdatedAt:  time.Now(),
		HasEvents:  true,
	}})

	require.NotPanics(t, func() { _ = b.String() })
	b.SetSize(100, 20)
	require.NotPanics(t, func() { _ = b.String() })
	out := b.String()
	require.Contains(t, out, "Missing Events")
	require.Contains(t, out, "Repository")
}

func TestSessionBrowserListScrollsToSelection(t *testing.T) {
	b := NewSessionBrowser()
	now := time.Now()
	sessions := make([]copilot.Session, 0, 30)
	for i := 0; i < 30; i++ {
		sessions = append(sessions, copilot.Session{
			ID:        fmt.Sprintf("s%02d", i),
			Name:      fmt.Sprintf("Session %02d", i),
			UpdatedAt: now.Add(-time.Duration(i) * time.Minute),
			HasEvents: true,
		})
	}
	b.SetSessions(sessions)
	b.SetSize(120, 12) // small viewport: the selection scrolls into view

	// Select the oldest row, far past the fold.
	b.selectedIdx = 29
	out := b.String()
	require.Contains(t, out, "Session 29", "selected row past the fold must be visible")
	require.NotContains(t, out, "Session 00", "top rows should scroll out of view")
}

func TestSessionBrowserPreviewLoadsSnippet(t *testing.T) {
	dir := t.TempDir()
	events := `{"type":"user.message","data":{"content":"Implement the session browser preview"}}` + "\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "events.jsonl"), []byte(events), 0o644))

	b := NewSessionBrowser()
	b.SetSize(160, 24)
	b.SetSessions([]copilot.Session{{
		ID:        "with-events",
		Dir:       dir,
		Name:      "Has Events",
		UpdatedAt: time.Now(),
		HasEvents: true,
	}})

	out := b.String()
	require.Contains(t, out, "Implement", "preview should load the first user message snippet")
	require.NotContains(t, out, "No cached snippet loaded", "snippet must be populated, not the fallback")
}
