package config

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestState_SidebarModeJSONRoundTrip(t *testing.T) {
	s := DefaultState()
	require.Equal(t, 0, s.GetSidebarMode())

	s.SidebarMode = 2
	data, err := json.Marshal(s)
	require.NoError(t, err)

	var loaded State
	require.NoError(t, json.Unmarshal(data, &loaded))
	require.Equal(t, 2, loaded.GetSidebarMode())
}

func TestState_SidebarModeBackCompatMissingField(t *testing.T) {
	// Old state.json had no sidebar_mode field; it must default to 0 (Manual).
	old := []byte(`{"help_screens_seen": 3, "instances": []}`)

	var loaded State
	require.NoError(t, json.Unmarshal(old, &loaded))
	require.Equal(t, 0, loaded.GetSidebarMode())
	require.Equal(t, uint32(3), loaded.GetHelpScreensSeen())
}

func TestState_DefaultStateSidebarModeIsManual(t *testing.T) {
	require.Equal(t, 0, DefaultState().GetSidebarMode())
}
