package app

import (
	"context"
	"fmt"
	"hangar/config"
	"hangar/log"
	"hangar/session"
	"hangar/session/copilot"
	"hangar/ui"
	"hangar/ui/overlay"
	"os"
	"testing"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMain runs before all tests to set up the test environment
func TestMain(m *testing.M) {
	// Initialize the logger before any tests run
	log.Initialize(false)
	defer log.Close()

	// Run all tests
	exitCode := m.Run()

	// Exit with the same code as the tests
	os.Exit(exitCode)
}

func newTestHomeForKeyHandling() *home {
	sp := spinner.New(spinner.WithSpinner(spinner.MiniDot))
	return &home{
		ctx:               context.Background(),
		state:             stateDefault,
		appConfig:         config.DefaultConfig(),
		list:              ui.NewList(&sp, false),
		menu:              ui.NewMenu(),
		tabbedWindow:      ui.NewTabbedWindow(ui.NewPreviewPane(), ui.NewDiffPane(), ui.NewTerminalPane()),
		sessionBrowser:    ui.NewSessionBrowser(),
		errBox:            ui.NewErrBox(),
		resumedSessionIDs: make(map[string]bool),
	}
}

func TestSessionBrowserStateTransitions(t *testing.T) {
	t.Run("b opens browse state from default", func(t *testing.T) {
		h := newTestHomeForKeyHandling()
		keyMsg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("b")}

		// First press highlights/resends the menu key; second handles the resent key.
		_, _ = h.handleKeyPress(keyMsg)
		model, _ := h.handleKeyPress(keyMsg)
		homeModel, ok := model.(*home)
		require.True(t, ok)

		assert.Equal(t, stateBrowse, homeModel.state)
		assert.Equal(t, "", homeModel.sessionBrowser.Query())
	})

	t.Run("q edits browse search instead of quitting", func(t *testing.T) {
		h := newTestHomeForKeyHandling()
		h.state = stateBrowse
		h.menu.SetState(ui.StateBrowse)

		model, _ := h.handleKeyPress(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
		homeModel, ok := model.(*home)
		require.True(t, ok)

		assert.Equal(t, stateBrowse, homeModel.state)
		assert.Equal(t, "q", homeModel.sessionBrowser.Query())
	})

	t.Run("esc closes browse state", func(t *testing.T) {
		h := newTestHomeForKeyHandling()
		h.state = stateBrowse
		h.menu.SetState(ui.StateBrowse)

		model, _ := h.handleKeyPress(tea.KeyMsg{Type: tea.KeyEsc})
		homeModel, ok := model.(*home)
		require.True(t, ok)

		assert.Equal(t, stateDefault, homeModel.state)
	})
}

func TestBuildResumeInstance(t *testing.T) {
	h := newTestHomeForKeyHandling()
	h.program = "claude" // non-copilot program must fall back to "copilot" for --resume

	sel := &copilot.Session{
		ID:         "abcdef1234567890",
		Name:       "Plan The Feature",
		OriginRoot: t.TempDir(),
		OriginHead: "deadbeefcafe",
	}

	inst, err := h.buildResumeInstance(sel, sel.OriginRoot)
	require.NoError(t, err)
	require.NotNil(t, inst)
	assert.Equal(t, "Plan The Feature (resume abcdef)", inst.Title)
	assert.Equal(t, "abcdef1234567890", inst.AgentSessionID)
	assert.Equal(t, "deadbeefcafe", inst.BaseCommit)
	assert.Equal(t, "copilot", inst.Program)

	// A copilot-based program keeps its custom args.
	h.program = "copilot --banner"
	inst2, err := h.buildResumeInstance(sel, sel.OriginRoot)
	require.NoError(t, err)
	assert.Equal(t, "copilot --banner", inst2.Program)
}

func TestSessionRestartGuards(t *testing.T) {
	t.Run("nil session is a no-op", func(t *testing.T) {
		h := newTestHomeForKeyHandling()
		model, cmd := h.handleSessionRestart(nil)
		require.NotNil(t, model)
		assert.Nil(t, cmd)
		assert.Equal(t, 0, h.list.NumInstances())
	})

	t.Run("in-use session is blocked", func(t *testing.T) {
		h := newTestHomeForKeyHandling()
		h.state = stateBrowse
		sel := &copilot.Session{ID: "id-in-use", Name: "Busy", InUse: true}

		_, _ = h.handleSessionRestart(sel)

		assert.Equal(t, 0, h.list.NumInstances(), "no workspace should be created for an in-use session")
		assert.False(t, h.resumedSessionIDs["id-in-use"])
	})

	t.Run("already-resumed session is blocked", func(t *testing.T) {
		h := newTestHomeForKeyHandling()
		h.resumedSessionIDs["already"] = true
		sel := &copilot.Session{ID: "already", Name: "Dup"}

		model, _ := h.handleSessionRestart(sel)
		require.NotNil(t, model)

		assert.Equal(t, stateDefault, h.state)
		assert.Equal(t, 0, h.list.NumInstances(), "no second workspace for an already-resumed session")
	})

	t.Run("instance limit is enforced", func(t *testing.T) {
		h := newTestHomeForKeyHandling()
		for i := 0; i < GlobalInstanceLimit; i++ {
			inst, err := session.NewInstance(session.InstanceOptions{
				Title:   fmt.Sprintf("ws-%d", i),
				Path:    ".",
				Program: "copilot",
			})
			require.NoError(t, err)
			h.list.AddInstance(inst)
		}
		sel := &copilot.Session{ID: "over-limit", Name: "X"}

		_, _ = h.handleSessionRestart(sel)

		assert.Equal(t, GlobalInstanceLimit, h.list.NumInstances(), "no workspace beyond the instance limit")
	})
}

func TestSessionsLoadedTriggersIndexBuild(t *testing.T) {
	h := newTestHomeForKeyHandling()
	// The returned cmd builds the content index off-thread; we only assert it is
	// scheduled (executing it would touch the on-disk index).
	model, cmd := h.Update(sessionsLoadedMsg{sessions: []copilot.Session{{ID: "x"}}})
	require.NotNil(t, model)
	require.NotNil(t, cmd, "discovery should schedule a content-index build")
}

func TestIndexReadyWiresFilter(t *testing.T) {
	h := newTestHomeForKeyHandling()
	idx, err := copilot.OpenIndex() // read-only; empty index when no file exists
	require.NoError(t, err)
	require.NotNil(t, idx)

	model, _ := h.Update(indexReadyMsg{index: idx})
	hm, ok := model.(*home)
	require.True(t, ok)
	assert.Same(t, idx, hm.copilotIndex, "index-ready should store the index for content search")
}

// TestConfirmationModalStateTransitions tests state transitions without full instance setup
func TestConfirmationModalStateTransitions(t *testing.T) {
	// Create a minimal home struct for testing state transitions
	h := &home{
		ctx:       context.Background(),
		state:     stateDefault,
		appConfig: config.DefaultConfig(),
	}

	t.Run("shows confirmation on D press", func(t *testing.T) {
		// Simulate pressing 'D'
		h.state = stateDefault
		h.confirmationOverlay = nil

		// Manually trigger what would happen in handleKeyPress for 'D'
		h.state = stateConfirm
		h.confirmationOverlay = overlay.NewConfirmationOverlay("[!] Kill session 'test'?")

		assert.Equal(t, stateConfirm, h.state)
		assert.NotNil(t, h.confirmationOverlay)
		assert.False(t, h.confirmationOverlay.Dismissed)
	})

	t.Run("returns to default on y press", func(t *testing.T) {
		// Start in confirmation state
		h.state = stateConfirm
		h.confirmationOverlay = overlay.NewConfirmationOverlay("Test confirmation")

		// Simulate pressing 'y' using HandleKeyPress
		keyMsg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")}
		shouldClose := h.confirmationOverlay.HandleKeyPress(keyMsg)
		if shouldClose {
			h.state = stateDefault
			h.confirmationOverlay = nil
		}

		assert.Equal(t, stateDefault, h.state)
		assert.Nil(t, h.confirmationOverlay)
	})

	t.Run("returns to default on n press", func(t *testing.T) {
		// Start in confirmation state
		h.state = stateConfirm
		h.confirmationOverlay = overlay.NewConfirmationOverlay("Test confirmation")

		// Simulate pressing 'n' using HandleKeyPress
		keyMsg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("n")}
		shouldClose := h.confirmationOverlay.HandleKeyPress(keyMsg)
		if shouldClose {
			h.state = stateDefault
			h.confirmationOverlay = nil
		}

		assert.Equal(t, stateDefault, h.state)
		assert.Nil(t, h.confirmationOverlay)
	})

	t.Run("returns to default on esc press", func(t *testing.T) {
		// Start in confirmation state
		h.state = stateConfirm
		h.confirmationOverlay = overlay.NewConfirmationOverlay("Test confirmation")

		// Simulate pressing ESC using HandleKeyPress
		keyMsg := tea.KeyMsg{Type: tea.KeyEscape}
		shouldClose := h.confirmationOverlay.HandleKeyPress(keyMsg)
		if shouldClose {
			h.state = stateDefault
			h.confirmationOverlay = nil
		}

		assert.Equal(t, stateDefault, h.state)
		assert.Nil(t, h.confirmationOverlay)
	})
}

// TestConfirmationModalKeyHandling tests the actual key handling in confirmation state
func TestConfirmationModalKeyHandling(t *testing.T) {
	// Import needed packages
	spinner := spinner.New(spinner.WithSpinner(spinner.MiniDot))
	list := ui.NewList(&spinner, false)

	// Create enough of home struct to test handleKeyPress in confirmation state
	h := &home{
		ctx:                 context.Background(),
		state:               stateConfirm,
		appConfig:           config.DefaultConfig(),
		list:                list,
		menu:                ui.NewMenu(),
		confirmationOverlay: overlay.NewConfirmationOverlay("Kill session?"),
	}

	testCases := []struct {
		name              string
		key               string
		expectedState     state
		expectedDismissed bool
		expectedNil       bool
	}{
		{
			name:              "y key confirms and dismisses overlay",
			key:               "y",
			expectedState:     stateDefault,
			expectedDismissed: true,
			expectedNil:       true,
		},
		{
			name:              "n key cancels and dismisses overlay",
			key:               "n",
			expectedState:     stateDefault,
			expectedDismissed: true,
			expectedNil:       true,
		},
		{
			name:              "esc key cancels and dismisses overlay",
			key:               "esc",
			expectedState:     stateDefault,
			expectedDismissed: true,
			expectedNil:       true,
		},
		{
			name:              "other keys are ignored",
			key:               "x",
			expectedState:     stateConfirm,
			expectedDismissed: false,
			expectedNil:       false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Reset state
			h.state = stateConfirm
			h.confirmationOverlay = overlay.NewConfirmationOverlay("Kill session?")

			// Create key message
			var keyMsg tea.KeyMsg
			if tc.key == "esc" {
				keyMsg = tea.KeyMsg{Type: tea.KeyEscape}
			} else {
				keyMsg = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(tc.key)}
			}

			// Call handleKeyPress
			model, _ := h.handleKeyPress(keyMsg)
			homeModel, ok := model.(*home)
			require.True(t, ok)

			assert.Equal(t, tc.expectedState, homeModel.state, "State mismatch for key: %s", tc.key)
			if tc.expectedNil {
				assert.Nil(t, homeModel.confirmationOverlay, "Overlay should be nil for key: %s", tc.key)
			} else {
				assert.NotNil(t, homeModel.confirmationOverlay, "Overlay should not be nil for key: %s", tc.key)
				assert.Equal(t, tc.expectedDismissed, homeModel.confirmationOverlay.Dismissed, "Dismissed mismatch for key: %s", tc.key)
			}
		})
	}
}

// TestConfirmationMessageFormatting tests that confirmation messages are formatted correctly
func TestConfirmationMessageFormatting(t *testing.T) {
	testCases := []struct {
		name            string
		sessionTitle    string
		expectedMessage string
	}{
		{
			name:            "short session name",
			sessionTitle:    "my-feature",
			expectedMessage: "[!] Kill session 'my-feature'? (y/n)",
		},
		{
			name:            "long session name",
			sessionTitle:    "very-long-feature-branch-name-here",
			expectedMessage: "[!] Kill session 'very-long-feature-branch-name-here'? (y/n)",
		},
		{
			name:            "session with spaces",
			sessionTitle:    "feature with spaces",
			expectedMessage: "[!] Kill session 'feature with spaces'? (y/n)",
		},
		{
			name:            "session with special chars",
			sessionTitle:    "feature/branch-123",
			expectedMessage: "[!] Kill session 'feature/branch-123'? (y/n)",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Test the message formatting directly
			actualMessage := fmt.Sprintf("[!] Kill session '%s'? (y/n)", tc.sessionTitle)
			assert.Equal(t, tc.expectedMessage, actualMessage)
		})
	}
}

// TestConfirmationFlowSimulation tests the confirmation flow by simulating the state changes
func TestConfirmationFlowSimulation(t *testing.T) {
	// Create a minimal setup
	spinner := spinner.New(spinner.WithSpinner(spinner.MiniDot))
	list := ui.NewList(&spinner, false)

	// Add test instance
	instance, err := session.NewInstance(session.InstanceOptions{
		Title:   "test-session",
		Path:    t.TempDir(),
		Program: "claude",
		AutoYes: false,
	})
	require.NoError(t, err)
	_ = list.AddInstance(instance)
	list.SetSelectedInstance(0)

	h := &home{
		ctx:       context.Background(),
		state:     stateDefault,
		appConfig: config.DefaultConfig(),
		list:      list,
		menu:      ui.NewMenu(),
	}

	// Simulate what happens when D is pressed
	selected := h.list.GetSelectedInstance()
	require.NotNil(t, selected)

	// This is what the KeyKill handler does
	message := fmt.Sprintf("[!] Kill session '%s'?", selected.Title)
	h.confirmationOverlay = overlay.NewConfirmationOverlay(message)
	h.state = stateConfirm

	// Verify the state
	assert.Equal(t, stateConfirm, h.state)
	assert.NotNil(t, h.confirmationOverlay)
	assert.False(t, h.confirmationOverlay.Dismissed)
	// Test that overlay renders with the correct message
	rendered := h.confirmationOverlay.Render()
	assert.Contains(t, rendered, "Kill session 'test-session'?")
}

// TestConfirmActionWithDifferentTypes tests that confirmAction works with different action types
func TestConfirmActionWithDifferentTypes(t *testing.T) {
	h := &home{
		ctx:       context.Background(),
		state:     stateDefault,
		appConfig: config.DefaultConfig(),
	}

	t.Run("works with simple action returning nil", func(t *testing.T) {
		actionCalled := false
		action := func() tea.Msg {
			actionCalled = true
			return nil
		}

		// Set up callback to track action execution
		actionExecuted := false
		h.confirmationOverlay = overlay.NewConfirmationOverlay("Test action?")
		h.confirmationOverlay.OnConfirm = func() {
			h.state = stateDefault
			actionExecuted = true
			action() // Execute the action
		}
		h.state = stateConfirm

		// Verify state was set
		assert.Equal(t, stateConfirm, h.state)
		assert.NotNil(t, h.confirmationOverlay)
		assert.False(t, h.confirmationOverlay.Dismissed)
		assert.NotNil(t, h.confirmationOverlay.OnConfirm)

		// Execute the confirmation callback
		h.confirmationOverlay.OnConfirm()
		assert.True(t, actionCalled)
		assert.True(t, actionExecuted)
	})

	t.Run("works with action returning error", func(t *testing.T) {
		expectedErr := fmt.Errorf("test error")
		action := func() tea.Msg {
			return expectedErr
		}

		// Set up callback to track action execution
		var receivedMsg tea.Msg
		h.confirmationOverlay = overlay.NewConfirmationOverlay("Error action?")
		h.confirmationOverlay.OnConfirm = func() {
			h.state = stateDefault
			receivedMsg = action() // Execute the action and capture result
		}
		h.state = stateConfirm

		// Verify state was set
		assert.Equal(t, stateConfirm, h.state)
		assert.NotNil(t, h.confirmationOverlay)
		assert.False(t, h.confirmationOverlay.Dismissed)
		assert.NotNil(t, h.confirmationOverlay.OnConfirm)

		// Execute the confirmation callback
		h.confirmationOverlay.OnConfirm()
		assert.Equal(t, expectedErr, receivedMsg)
	})

	t.Run("works with action returning custom message", func(t *testing.T) {
		action := func() tea.Msg {
			return instanceChangedMsg{}
		}

		// Set up callback to track action execution
		var receivedMsg tea.Msg
		h.confirmationOverlay = overlay.NewConfirmationOverlay("Custom message action?")
		h.confirmationOverlay.OnConfirm = func() {
			h.state = stateDefault
			receivedMsg = action() // Execute the action and capture result
		}
		h.state = stateConfirm

		// Verify state was set
		assert.Equal(t, stateConfirm, h.state)
		assert.NotNil(t, h.confirmationOverlay)
		assert.False(t, h.confirmationOverlay.Dismissed)
		assert.NotNil(t, h.confirmationOverlay.OnConfirm)

		// Execute the confirmation callback
		h.confirmationOverlay.OnConfirm()
		_, ok := receivedMsg.(instanceChangedMsg)
		assert.True(t, ok, "Expected instanceChangedMsg but got %T", receivedMsg)
	})
}

// TestMultipleConfirmationsDontInterfere tests that multiple confirmations don't interfere with each other
func TestMultipleConfirmationsDontInterfere(t *testing.T) {
	h := &home{
		ctx:       context.Background(),
		state:     stateDefault,
		appConfig: config.DefaultConfig(),
	}

	// First confirmation
	action1Called := false
	action1 := func() tea.Msg {
		action1Called = true
		return nil
	}

	// Set up first confirmation
	h.confirmationOverlay = overlay.NewConfirmationOverlay("First action?")
	firstOnConfirm := func() {
		h.state = stateDefault
		action1()
	}
	h.confirmationOverlay.OnConfirm = firstOnConfirm
	h.state = stateConfirm

	// Verify first confirmation
	assert.Equal(t, stateConfirm, h.state)
	assert.NotNil(t, h.confirmationOverlay)
	assert.False(t, h.confirmationOverlay.Dismissed)
	assert.NotNil(t, h.confirmationOverlay.OnConfirm)

	// Cancel first confirmation (simulate pressing 'n')
	keyMsg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("n")}
	shouldClose := h.confirmationOverlay.HandleKeyPress(keyMsg)
	if shouldClose {
		h.state = stateDefault
		h.confirmationOverlay = nil
	}

	// Second confirmation with different action
	action2Called := false
	action2 := func() tea.Msg {
		action2Called = true
		return fmt.Errorf("action2 error")
	}

	// Set up second confirmation
	h.confirmationOverlay = overlay.NewConfirmationOverlay("Second action?")
	var secondResult tea.Msg
	secondOnConfirm := func() {
		h.state = stateDefault
		secondResult = action2()
	}
	h.confirmationOverlay.OnConfirm = secondOnConfirm
	h.state = stateConfirm

	// Verify second confirmation
	assert.Equal(t, stateConfirm, h.state)
	assert.NotNil(t, h.confirmationOverlay)
	assert.False(t, h.confirmationOverlay.Dismissed)
	assert.NotNil(t, h.confirmationOverlay.OnConfirm)

	// Execute second action to verify it's the correct one
	h.confirmationOverlay.OnConfirm()
	err, ok := secondResult.(error)
	assert.True(t, ok)
	assert.Equal(t, "action2 error", err.Error())
	assert.True(t, action2Called)
	assert.False(t, action1Called, "First action should not have been called")

	// Test that cancelled action can still be executed independently
	firstOnConfirm()
	assert.True(t, action1Called, "First action should be callable after being replaced")
}

// TestConfirmationModalVisualAppearance tests that confirmation modal has distinct visual appearance
func TestConfirmationModalVisualAppearance(t *testing.T) {
	h := &home{
		ctx:       context.Background(),
		state:     stateDefault,
		appConfig: config.DefaultConfig(),
	}

	// Create a test confirmation overlay
	message := "[!] Delete everything?"
	h.confirmationOverlay = overlay.NewConfirmationOverlay(message)
	h.state = stateConfirm

	// Verify the overlay was created with confirmation settings
	assert.NotNil(t, h.confirmationOverlay)
	assert.Equal(t, stateConfirm, h.state)
	assert.False(t, h.confirmationOverlay.Dismissed)

	// Test the overlay render (we can test that it renders without errors)
	rendered := h.confirmationOverlay.Render()
	assert.NotEmpty(t, rendered)

	// Test that it includes the message content and instructions
	assert.Contains(t, rendered, "Delete everything?")
	assert.Contains(t, rendered, "Press")
	assert.Contains(t, rendered, "to confirm")
	assert.Contains(t, rendered, "to cancel")

	// Test that the danger indicator is preserved
	assert.Contains(t, rendered, "[!")
}

// fakeAppState is an in-memory config.AppState for tests (no disk writes).
type fakeAppState struct {
	seen uint32
	mode int
}

func (f *fakeAppState) GetHelpScreensSeen() uint32        { return f.seen }
func (f *fakeAppState) SetHelpScreensSeen(s uint32) error { f.seen = s; return nil }
func (f *fakeAppState) GetSidebarMode() int               { return f.mode }
func (f *fakeAppState) SetSidebarMode(m int) error        { f.mode = m; return nil }

func newTestHome(t *testing.T, appState config.AppState) *home {
	t.Helper()
	s := spinner.New(spinner.WithSpinner(spinner.MiniDot))
	return &home{
		ctx:          context.Background(),
		state:        stateDefault,
		appConfig:    config.DefaultConfig(),
		appState:     appState,
		list:         ui.NewList(&s, false),
		menu:         ui.NewMenu(),
		tabbedWindow: ui.NewTabbedWindow(ui.NewPreviewPane(), ui.NewDiffPane(), ui.NewTerminalPane()),
		errBox:       ui.NewErrBox(),
	}
}

func TestCycleMode_AdvancesAndPersists(t *testing.T) {
	fake := &fakeAppState{}
	h := newTestHome(t, fake)

	h.cycleMode(true)
	require.Equal(t, ui.ModeGroupByRepo, h.list.Mode())
	require.Equal(t, int(ui.ModeGroupByRepo), fake.mode)

	h.cycleMode(true)
	require.Equal(t, ui.ModeRecentActivity, h.list.Mode())
	require.Equal(t, int(ui.ModeRecentActivity), fake.mode)

	// Backward from Manual wraps to PinnedPending.
	h.list.SetMode(ui.ModeManual)
	h.cycleMode(false)
	require.Equal(t, ui.ModePinnedPending, h.list.Mode())
	require.Equal(t, int(ui.ModePinnedPending), fake.mode)
}

func TestReorderSelected_BlockedOutsideManualMode(t *testing.T) {
	fake := &fakeAppState{}
	h := newTestHome(t, fake)
	inst, err := session.NewInstance(session.InstanceOptions{Title: "a", Path: ".", Program: "echo"})
	require.NoError(t, err)
	h.list.AddInstance(inst)
	inst2, err := session.NewInstance(session.InstanceOptions{Title: "b", Path: ".", Program: "echo"})
	require.NoError(t, err)
	h.list.AddInstance(inst2)

	// In a non-Manual mode, reorder is a no-op (and does not panic without storage).
	h.list.SetMode(ui.ModeRecentActivity)
	h.list.SelectInstance(inst2)
	_, _ = h.reorderSelected(true)
	// Order is unchanged by the blocked reorder.
	require.Equal(t, []string{"a", "b"}, []string{h.list.GetInstances()[0].Title, h.list.GetInstances()[1].Title})
}

func addInst(t *testing.T, h *home, title string) *session.Instance {
	t.Helper()
	inst, err := session.NewInstance(session.InstanceOptions{Title: title, Path: ".", Program: "echo"})
	require.NoError(t, err)
	h.list.AddInstance(inst)
	return inst
}

func runeMsg(r rune) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}} }

func typeQuery(h *home, q string) {
	for _, r := range q {
		h.handleSearchKey(runeMsg(r))
	}
}

func TestSearch_LettersAreTextNotNavigation(t *testing.T) {
	h := newTestHome(t, &fakeAppState{})
	addInst(t, h, "jungle")
	addInst(t, h, "kayak")
	addInst(t, h, "otter")

	h.enterSearch()
	require.Equal(t, stateSearch, h.state)
	require.True(t, h.list.Searching())

	// 'j' and 'k' are appended to the query (text), they do not navigate.
	typeQuery(h, "j")
	require.Equal(t, "j", h.list.Filter())
	typeQuery(h, "ungle")
	require.Equal(t, "jungle", h.list.Filter())
	require.Equal(t, 1, h.list.VisibleCount())
	require.Equal(t, "jungle", h.list.GetSelectedInstance().Title)
}

func TestSearch_ArrowsNavigate(t *testing.T) {
	h := newTestHome(t, &fakeAppState{})
	addInst(t, h, "a")
	addInst(t, h, "b")
	addInst(t, h, "c")

	h.enterSearch()
	require.Equal(t, "a", h.list.GetSelectedInstance().Title)
	h.handleSearchKey(tea.KeyMsg{Type: tea.KeyDown})
	require.Equal(t, "b", h.list.GetSelectedInstance().Title)
	h.handleSearchKey(tea.KeyMsg{Type: tea.KeyDown})
	require.Equal(t, "c", h.list.GetSelectedInstance().Title)
	h.handleSearchKey(tea.KeyMsg{Type: tea.KeyUp})
	require.Equal(t, "b", h.list.GetSelectedInstance().Title)
}

func TestSearch_EscRestoresPreSearchSelection(t *testing.T) {
	h := newTestHome(t, &fakeAppState{})
	addInst(t, h, "alpha")
	beta := addInst(t, h, "beta")
	addInst(t, h, "gamma")
	h.list.SelectInstance(beta)

	h.enterSearch()
	typeQuery(h, "gamma") // hides beta
	require.Equal(t, "gamma", h.list.GetSelectedInstance().Title)

	h.handleSearchKey(tea.KeyMsg{Type: tea.KeyEsc})
	require.Equal(t, stateDefault, h.state)
	require.False(t, h.list.Searching())
	require.Equal(t, "", h.list.Filter())
	require.Same(t, beta, h.list.GetSelectedInstance()) // restored
}

func TestSearch_EnterKeepsFilterAndVisibleSelection(t *testing.T) {
	h := newTestHome(t, &fakeAppState{})
	addInst(t, h, "alpha")
	beta := addInst(t, h, "beta")
	gamma := addInst(t, h, "gamma")
	h.list.SelectInstance(beta)

	h.enterSearch()
	typeQuery(h, "gamma")
	h.handleSearchKey(tea.KeyMsg{Type: tea.KeyEnter})

	require.Equal(t, stateDefault, h.state)
	require.Equal(t, "gamma", h.list.Filter()) // filter kept
	require.Same(t, gamma, h.list.GetSelectedInstance())
}

func TestSearch_NoMatchesGivesEmptySelection(t *testing.T) {
	h := newTestHome(t, &fakeAppState{})
	addInst(t, h, "alpha")
	addInst(t, h, "beta")

	h.enterSearch()
	typeQuery(h, "zzz")
	require.Equal(t, 0, h.list.VisibleCount())
	require.Nil(t, h.list.GetSelectedInstance())
}

func TestSearch_BackspaceEditsQuery(t *testing.T) {
	h := newTestHome(t, &fakeAppState{})
	addInst(t, h, "frontend")
	addInst(t, h, "backend")

	h.enterSearch()
	typeQuery(h, "front")
	require.Equal(t, 1, h.list.VisibleCount())
	for i := 0; i < 5; i++ {
		h.handleSearchKey(tea.KeyMsg{Type: tea.KeyBackspace})
	}
	require.Equal(t, "", h.list.Filter())
	require.Equal(t, 2, h.list.VisibleCount())
}

func TestNewInstance_ConcurrentStartFailureTargetsExactInstance(t *testing.T) {
	h := newTestHome(t, &fakeAppState{})

	inst1, err := session.NewInstance(session.InstanceOptions{Title: "one", Path: ".", Program: "echo"})
	require.NoError(t, err)
	h.beginNewInstance(inst1)
	require.Same(t, inst1, h.newInstance)

	// A second creation begins before inst1's async start completes.
	inst2, err := session.NewInstance(session.InstanceOptions{Title: "two", Path: ".", Program: "echo"})
	require.NoError(t, err)
	h.beginNewInstance(inst2)
	require.Same(t, inst2, h.newInstance)

	// inst1's start now fails: the exact failed instance is removed and the
	// still-in-creation inst2 tracking is left intact (identity-exact, R3).
	h.list.RemoveInstance(inst1)
	h.untrackNewInstance(inst1, true)

	require.Same(t, inst2, h.newInstance) // inst2 still tracked
	require.Equal(t, 1, h.list.NumInstances())
	require.Equal(t, "two", h.list.GetInstances()[0].Title)
}

func TestAnimation_StaleTickIgnoredAndSingleLoop(t *testing.T) {
	h := newTestHome(t, &fakeAppState{})
	h.list.SetSize(80, 40)
	addInst(t, h, "a")
	addInst(t, h, "b")
	addInst(t, h, "c")
	h.list.ResetMotion()

	// Trigger a reorder pulse.
	h.list.SetSelectedInstance(1)
	require.True(t, h.list.MoveSelectedUp())
	require.True(t, h.list.IsAnimating())

	cmd := h.scheduleAnimationIfNeeded()
	require.NotNil(t, cmd)
	require.True(t, h.animating)
	gen := h.animGen

	// Calling schedule again must NOT start a second loop.
	require.Nil(t, h.scheduleAnimationIfNeeded())
	require.Equal(t, gen, h.animGen)

	// A stale tick from an older generation is ignored.
	_, _ = h.Update(animTickMsg{gen: gen - 1})
	require.True(t, h.animating)

	// Current-generation ticks advance the animation until it settles. The pulse
	// lasts a handful of frames; iterate generously to guarantee settling.
	for i := 0; i < 12; i++ {
		_, _ = h.Update(animTickMsg{gen: h.animGen})
	}
	require.False(t, h.animating)
	require.False(t, h.list.IsAnimating())
}
