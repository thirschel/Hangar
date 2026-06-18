package config

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestSaveConfigRestrictedPermissions asserts that saveConfig creates the config
// directory with mode 0700 and config.json with mode 0600 (F-15).
//
// On Windows permission bits are controlled by NTFS ACLs rather than Unix mode
// bits, so this test is skipped there.
func TestSaveConfigRestrictedPermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix permission bits are not meaningful on Windows")
	}

	tempHome := t.TempDir()
	setTestHome(t, tempHome)

	cfg := DefaultConfig()
	require.NoError(t, saveConfig(cfg))

	configDir := filepath.Join(tempHome, ".hangar")
	configFile := filepath.Join(configDir, ConfigFileName)

	dirInfo, err := os.Stat(configDir)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0700), dirInfo.Mode().Perm(),
		"~/.hangar must be created with mode 0700")

	fileInfo, err := os.Stat(configFile)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0600), fileInfo.Mode().Perm(),
		"config.json must be created with mode 0600")
}

// TestSaveStateRestrictedPermissions asserts that SaveState creates the config
// directory with mode 0700 and state.json with mode 0600 (F-15).
func TestSaveStateRestrictedPermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix permission bits are not meaningful on Windows")
	}

	tempHome := t.TempDir()
	setTestHome(t, tempHome)

	state := DefaultState()
	require.NoError(t, SaveState(state))

	configDir := filepath.Join(tempHome, ".hangar")
	stateFile := filepath.Join(configDir, StateFileName)

	dirInfo, err := os.Stat(configDir)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0700), dirInfo.Mode().Perm(),
		"~/.hangar must be created with mode 0700")

	fileInfo, err := os.Stat(stateFile)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0600), fileInfo.Mode().Perm(),
		"state.json must be created with mode 0600")
}
