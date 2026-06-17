package ui

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMenu_SetInstanceDoesNotResetSearchState(t *testing.T) {
	m := NewMenu()
	m.SetState(StateSearch)
	require.Equal(t, StateSearch, m.state)

	// A background instance update (metadata tick) must not clobber search state.
	m.SetInstance(nil)
	require.Equal(t, StateSearch, m.state)

	// Leaving search via SetState works normally.
	m.SetState(StateDefault)
	require.Equal(t, StateEmpty, statelessAfterSetInstance(m))
}

// statelessAfterSetInstance applies SetInstance(nil) and returns the resulting
// state; with no special state active it should fall back to StateEmpty.
func statelessAfterSetInstance(m *Menu) MenuState {
	m.SetInstance(nil)
	return m.state
}

func TestMenu_RendersSearchStateWithoutPanic(t *testing.T) {
	m := NewMenu()
	m.SetSize(80, 1)
	m.SetState(StateSearch)
	out := m.String()
	require.Contains(t, out, "clear")
	require.Contains(t, out, "apply")
}
