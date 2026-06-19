package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
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

// requireNoLeftoverTempFiles asserts that an atomic SaveState left no temp files
// (the "state-*.json.tmp" scratch files) behind in the config directory.
func requireNoLeftoverTempFiles(t *testing.T, configDir string) {
	t.Helper()
	entries, err := os.ReadDir(configDir)
	require.NoError(t, err)
	for _, e := range entries {
		require.False(t, strings.HasSuffix(e.Name(), ".tmp"),
			"unexpected leftover temp file in config dir: %s", e.Name())
	}
}

// requireStateMode asserts the persisted state.json keeps 0600. Windows does not
// implement Unix permission bits (os.Stat reports them from the read-only
// attribute, e.g. 0666 for a writable file), so there we only assert the file is
// writable rather than exactly 0600.
func requireStateMode(t *testing.T, statePath string) {
	t.Helper()
	info, err := os.Stat(statePath)
	require.NoError(t, err)
	if runtime.GOOS == "windows" {
		require.NotZero(t, info.Mode().Perm()&0200, "state.json should be writable")
	} else {
		require.Equal(t, os.FileMode(0600), info.Mode().Perm())
	}
}

func TestSaveState_AtomicWriteRoundTrip(t *testing.T) {
	tempHome := t.TempDir()
	setTestHome(t, tempHome)

	state := &State{
		HelpScreensSeen: 7,
		SidebarMode:     2,
		InstancesData:   json.RawMessage(`[{"id":"alpha"},{"id":"beta"}]`),
	}

	require.NoError(t, SaveState(state))

	configDir := filepath.Join(tempHome, ".hangar")
	statePath := filepath.Join(configDir, StateFileName)

	// The file on disk must contain valid, complete JSON.
	raw, err := os.ReadFile(statePath)
	require.NoError(t, err)
	var onDisk State
	require.NoError(t, json.Unmarshal(raw, &onDisk),
		"state.json should contain valid JSON")

	// LoadState must round-trip the values we saved.
	loaded := LoadState()
	require.Equal(t, state.HelpScreensSeen, loaded.GetHelpScreensSeen())
	require.Equal(t, state.SidebarMode, loaded.GetSidebarMode())
	require.JSONEq(t, string(state.InstancesData), string(loaded.GetInstances()))

	requireStateMode(t, statePath)
	requireNoLeftoverTempFiles(t, configDir)
}

func TestSaveState_AtomicReplacesExistingFile(t *testing.T) {
	tempHome := t.TempDir()
	setTestHome(t, tempHome)

	configDir := filepath.Join(tempHome, ".hangar")
	statePath := filepath.Join(configDir, StateFileName)

	state := &State{HelpScreensSeen: 1, SidebarMode: 1, InstancesData: json.RawMessage("[]")}
	require.NoError(t, SaveState(state))

	// Saving again must atomically replace the existing state.json. This
	// exercises os.Rename's replace-existing behavior, which matters most on
	// Windows where renaming onto an existing target is platform-specific.
	state.HelpScreensSeen = 9
	state.SidebarMode = 5
	require.NoError(t, SaveState(state))

	loaded := LoadState()
	require.Equal(t, uint32(9), loaded.GetHelpScreensSeen())
	require.Equal(t, 5, loaded.GetSidebarMode())

	requireStateMode(t, statePath)
	requireNoLeftoverTempFiles(t, configDir)
}
