package config

import (
	"encoding/json"
	"fmt"
	"hangar/log"
	"os"
	"path/filepath"
)

const (
	StateFileName     = "state.json"
	InstancesFileName = "instances.json"
)

// InstanceStorage handles instance-related operations
type InstanceStorage interface {
	// SaveInstances saves the raw instance data
	SaveInstances(instancesJSON json.RawMessage) error
	// GetInstances returns the raw instance data
	GetInstances() json.RawMessage
	// DeleteAllInstances removes all stored instances
	DeleteAllInstances() error
}

// AppState handles application-level state
type AppState interface {
	// GetHelpScreensSeen returns the bitmask of seen help screens
	GetHelpScreensSeen() uint32
	// SetHelpScreensSeen updates the bitmask of seen help screens
	SetHelpScreensSeen(seen uint32) error
	// GetSidebarMode returns the persisted sidebar view mode as an int. The UI
	// layer validates the value (unknown -> Manual); config stores it opaquely to
	// avoid a dependency on the ui package.
	GetSidebarMode() int
	// SetSidebarMode persists the sidebar view mode.
	SetSidebarMode(mode int) error
}

// StateManager combines instance storage and app state management
type StateManager interface {
	InstanceStorage
	AppState
}

// State represents the application state that persists between sessions
type State struct {
	// HelpScreensSeen is a bitmask tracking which help screens have been shown
	HelpScreensSeen uint32 `json:"help_screens_seen"`
	// SidebarMode is the persisted sidebar view mode (see ui.SidebarMode). Stored
	// as an int so config stays decoupled from the ui package. Missing in older
	// state.json -> 0 -> Manual.
	SidebarMode int `json:"sidebar_mode"`
	// Instances stores the serialized instance data as raw JSON
	InstancesData json.RawMessage `json:"instances"`
}

// DefaultState returns the default state
func DefaultState() *State {
	return &State{
		HelpScreensSeen: 0,
		InstancesData:   json.RawMessage("[]"),
	}
}

// LoadState loads the state from disk. If it cannot be done, we return the default state.
func LoadState() *State {
	configDir, err := GetConfigDir()
	if err != nil {
		log.ErrorLog.Printf("failed to get config directory: %v", err)
		return DefaultState()
	}

	statePath := filepath.Join(configDir, StateFileName)
	data, err := os.ReadFile(statePath)
	if err != nil {
		if os.IsNotExist(err) {
			// Create and save default state if file doesn't exist
			defaultState := DefaultState()
			if saveErr := SaveState(defaultState); saveErr != nil {
				log.WarningLog.Printf("failed to save default state: %v", saveErr)
			}
			return defaultState
		}

		log.WarningLog.Printf("failed to get state file: %v", err)
		return DefaultState()
	}

	var state State
	if err := json.Unmarshal(data, &state); err != nil {
		log.ErrorLog.Printf("failed to parse state file: %v", err)
		return DefaultState()
	}

	return &state
}

// SaveState saves the state to disk atomically. The serialized JSON is written
// to a temp file in the same directory (same filesystem, so the rename is
// atomic) and then renamed over state.json. This guarantees that a crash
// mid-write or a concurrent reader (the daemon, the Windows session host, or the
// TUI) never observes a truncated or partially written file.
func SaveState(state *State) error {
	configDir, err := GetConfigDir()
	if err != nil {
		return fmt.Errorf("failed to get config directory: %w", err)
	}

	if err := os.MkdirAll(configDir, 0700); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}

	statePath := filepath.Join(configDir, StateFileName)
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal state: %w", err)
	}

	return atomicWriteFile(configDir, statePath, data)
}

// atomicWriteFile writes data to a temp file in dir and then atomically renames
// it over path, leaving the final file with 0600 permissions. The temp file is
// always cleaned up on any failure path so a partial write never lingers.
func atomicWriteFile(dir, path string, data []byte) error {
	tmp, err := os.CreateTemp(dir, "state-*.json.tmp")
	if err != nil {
		return fmt.Errorf("failed to create temp state file: %w", err)
	}
	tmpPath := tmp.Name()

	// Remove the temp file on any early return. Cleared only after a successful
	// rename, at which point tmpPath no longer refers to an existing file.
	removeTmp := true
	defer func() {
		if removeTmp {
			_ = os.Remove(tmpPath)
		}
	}()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("failed to write temp state file: %w", err)
	}

	// Flush to stable storage before the rename so the renamed file has complete
	// contents even if the machine crashes immediately afterwards.
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("failed to sync temp state file: %w", err)
	}

	if err := tmp.Close(); err != nil {
		return fmt.Errorf("failed to close temp state file: %w", err)
	}

	// os.CreateTemp already creates the file with 0600, but set it explicitly so
	// the final state.json keeps 0600 regardless of platform or umask.
	if err := os.Chmod(tmpPath, 0600); err != nil {
		return fmt.Errorf("failed to set temp state file permissions: %w", err)
	}

	// os.Rename atomically replaces an existing destination on both Unix and
	// Windows (MoveFileEx with MOVEFILE_REPLACE_EXISTING), so readers always see
	// either the old or the new complete file.
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("failed to replace state file: %w", err)
	}

	removeTmp = false
	return nil
}

// InstanceStorage interface implementation

// SaveInstances saves the raw instance data
func (s *State) SaveInstances(instancesJSON json.RawMessage) error {
	s.InstancesData = instancesJSON
	return SaveState(s)
}

// GetInstances returns the raw instance data
func (s *State) GetInstances() json.RawMessage {
	return s.InstancesData
}

// DeleteAllInstances removes all stored instances
func (s *State) DeleteAllInstances() error {
	s.InstancesData = json.RawMessage("[]")
	return SaveState(s)
}

// AppState interface implementation

// GetHelpScreensSeen returns the bitmask of seen help screens
func (s *State) GetHelpScreensSeen() uint32 {
	return s.HelpScreensSeen
}

// SetHelpScreensSeen updates the bitmask of seen help screens
func (s *State) SetHelpScreensSeen(seen uint32) error {
	s.HelpScreensSeen = seen
	return SaveState(s)
}

// GetSidebarMode returns the persisted sidebar view mode as an int.
func (s *State) GetSidebarMode() int {
	return s.SidebarMode
}

// SetSidebarMode persists the sidebar view mode.
func (s *State) SetSidebarMode(mode int) error {
	s.SidebarMode = mode
	return SaveState(s)
}
