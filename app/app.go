package app

import (
	"claude-squad/config"
	"claude-squad/keys"
	"claude-squad/log"
	"claude-squad/session"
	"claude-squad/session/agentcmd"
	"claude-squad/session/copilot"
	"claude-squad/session/git"
	"claude-squad/session/winhost"
	"claude-squad/ui"
	"claude-squad/ui/overlay"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"
)

const GlobalInstanceLimit = 10

// Run is the main entrypoint into the application.
func Run(ctx context.Context, program string, autoYes bool) error {
	p := tea.NewProgram(
		newHome(ctx, program, autoYes),
		tea.WithAltScreen(),
		tea.WithMouseCellMotion(), // Mouse scroll
	)
	_, err := p.Run()
	return err
}

type state int

const (
	stateDefault state = iota
	// stateNew is the state when the user is creating a new instance.
	stateNew
	// statePrompt is the state when the user is entering a prompt.
	statePrompt
	// stateHelp is the state when a help screen is displayed.
	stateHelp
	// stateConfirm is the state when a confirmation modal is displayed.
	stateConfirm
	// stateSearch is the state when the sidebar search/filter input is open.
	stateSearch
	// stateBrowse is the state when the Copilot session browser is displayed.
	stateBrowse
)

type home struct {
	ctx context.Context

	// -- Storage and Configuration --

	program string
	autoYes bool

	// storage is the interface for saving/loading data to/from the app's state
	storage *session.Storage
	// appConfig stores persistent application configuration
	appConfig *config.Config
	// appState stores persistent application state like seen help screens
	appState config.AppState

	// -- State --

	// state is the current discrete state of the application
	state state
	// newInstanceFinalizer is called when the state is stateNew and then you press enter.
	// It registers the new instance in the list after the instance has been started.
	newInstanceFinalizer func()

	// newInstance is the just-created, not-yet-started instance being named (in
	// stateNew/statePrompt). Tracked by identity so cancel/Esc/Ctrl+C/start-failure
	// remove the exact instance regardless of the active mode/filter.
	newInstance *session.Instance

	// preSearchSelection is the instance selected when search was entered; it is
	// restored on Esc (R6).
	preSearchSelection *session.Instance
	// preNewFilter is the search filter suspended while creating a new instance,
	// restored when creation is cancelled (R6, §7.8).
	preNewFilter string

	// animating is true while a sidebar row animation tick loop is running.
	// animGen is the current animation generation; stale ticks (gen != animGen)
	// no-op so only one loop is ever live (R4).
	animating bool
	animGen   uint64

	// promptAfterName tracks if we should enter prompt mode after naming
	promptAfterName bool

	// keySent is used to manage underlining menu items
	keySent bool

	// instanceStarting is true while a background instance start is in progress.
	// Prevents double-submission and guards against interacting with a not-yet-started instance.
	instanceStarting bool
	// startingInstance holds a reference to the instance being started in the background.
	startingInstance *session.Instance
	// pendingAttach holds the instance to attach to once the attach help overlay
	// is dismissed (used by the native-Windows tea.Exec attach path).
	pendingAttach *session.Instance

	// -- UI Components --

	// list displays the list of instances
	list *ui.List
	// menu displays the bottom menu
	menu *ui.Menu
	// tabbedWindow displays the tabbed window with preview and diff panes
	tabbedWindow *ui.TabbedWindow
	// errBox displays error messages
	errBox *ui.ErrBox
	// global spinner instance. we plumb this down to where it's needed
	spinner spinner.Model
	// textInputOverlay handles text input with state
	textInputOverlay *overlay.TextInputOverlay
	// textOverlay displays text information
	textOverlay *overlay.TextOverlay
	// confirmationOverlay displays confirmation modals
	confirmationOverlay *overlay.ConfirmationOverlay
	// sessionBrowser displays discovered Copilot sessions
	sessionBrowser *ui.SessionBrowser
	// resumedSessionIDs tracks Copilot sessions already resumed into workspaces
	resumedSessionIDs map[string]bool
	// copilotIndex is the lazily-built content search index backing the browser
	copilotIndex *copilot.Index
}

func newHome(ctx context.Context, program string, autoYes bool) *home {
	// Load application config
	appConfig := config.LoadConfig()

	// Load application state
	appState := config.LoadState()

	// Initialize storage
	storage, err := session.NewStorage(appState)
	if err != nil {
		fmt.Printf("Failed to initialize storage: %v\n", err)
		os.Exit(1)
	}

	h := &home{
		ctx:               ctx,
		spinner:           spinner.New(spinner.WithSpinner(spinner.MiniDot)),
		menu:              ui.NewMenu(),
		tabbedWindow:      ui.NewTabbedWindow(ui.NewPreviewPane(), ui.NewDiffPane(), ui.NewTerminalPane()),
		errBox:            ui.NewErrBox(),
		storage:           storage,
		appConfig:         appConfig,
		program:           program,
		autoYes:           autoYes,
		state:             stateDefault,
		appState:          appState,
		sessionBrowser:    ui.NewSessionBrowser(),
		resumedSessionIDs: make(map[string]bool),
	}
	h.list = ui.NewList(&h.spinner, autoYes)
	// Restore the persisted sidebar mode (unknown values fall back to Manual).
	h.list.SetMode(ui.ValidSidebarMode(appState.GetSidebarMode()))
	// Sidebar motion is on unless disabled in config (also auto-disabled by size/count).
	h.list.SetMotionConfig(!appConfig.DisableSidebarMotion)

	// Load saved instances
	instances, err := storage.LoadInstances()
	if err != nil {
		if vm, ok := winhost.AsVersionMismatch(err); ok {
			fmt.Printf("A session-host from a different cs version is running "+
				"(host protocol v%d, this cs v%d).\n", vm.HostVersion, vm.ClientVersion)
			fmt.Println("This usually means cs was upgraded while an old session-host " +
				"(with running sessions) is still alive.")
			fmt.Println("Run `cs reset` to stop the old host (this ends any running " +
				"sessions), then start cs again.")
			os.Exit(1)
		}
		fmt.Printf("Failed to load instances: %v\n", err)
		os.Exit(1)
	}

	// Add loaded instances to the list
	for _, instance := range instances {
		// Call the finalizer immediately.
		h.list.AddInstance(instance)()
		if autoYes {
			instance.SetAutoYes(true)
		}
		// Re-seed the resumed-session guard so a session resumed before a restart is
		// recognized as already open (selecting it) instead of colliding on its branch.
		if instance.AgentSessionID != "" {
			h.resumedSessionIDs[instance.AgentSessionID] = true
		}
	}
	// Don't flash the initial paint: baseline the animator to the loaded layout.
	h.list.ResetMotion()

	return h
}

// updateHandleWindowSizeEvent sets the sizes of the components.
// The components will try to render inside their bounds.
func (m *home) updateHandleWindowSizeEvent(msg tea.WindowSizeMsg) {
	// List takes 30% of width, preview takes 70%
	listWidth := int(float32(msg.Width) * 0.3)
	tabsWidth := msg.Width - listWidth

	// Menu takes 10% of height, list and window take 90%
	contentHeight := int(float32(msg.Height) * 0.9)
	menuHeight := msg.Height - contentHeight - 1     // minus 1 for error box
	m.errBox.SetSize(int(float32(msg.Width)*0.9), 1) // error box takes 1 row

	m.tabbedWindow.SetSize(tabsWidth, contentHeight)
	m.list.SetSize(listWidth, contentHeight)
	m.sessionBrowser.SetSize(msg.Width, contentHeight)

	if m.textInputOverlay != nil {
		m.textInputOverlay.SetSize(int(float32(msg.Width)*0.6), int(float32(msg.Height)*0.4))
	}
	if m.textOverlay != nil {
		m.textOverlay.SetWidth(int(float32(msg.Width) * 0.6))
	}

	previewWidth, previewHeight := m.tabbedWindow.GetPreviewSize()
	if err := m.list.SetSessionPreviewSize(previewWidth, previewHeight); err != nil {
		log.ErrorLog.Print(err)
	}
	m.menu.SetSize(msg.Width, menuHeight)
}

func (m *home) Init() tea.Cmd {
	// Upon starting, we want to start the spinner. Whenever we get a spinner.TickMsg, we
	// update the spinner, which sends a new spinner.TickMsg. I think this lasts forever lol.
	return tea.Batch(
		m.spinner.Tick,
		func() tea.Msg {
			time.Sleep(100 * time.Millisecond)
			return previewTickMsg{}
		},
		tickUpdateMetadataCmd(m.snapshotActiveInstances(), m.list.GetSelectedInstance()),
	)
}

func (m *home) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case hideErrMsg:
		m.errBox.Clear()
	case previewTickMsg:
		cmd := m.instanceChanged()
		return m, tea.Batch(
			cmd,
			func() tea.Msg {
				time.Sleep(100 * time.Millisecond)
				return previewTickMsg{}
			},
		)
	case keyupMsg:
		m.menu.ClearKeydown()
		return m, nil
	case instanceStartDoneMsg:
		m.instanceStarting = false
		inst := msg.instance
		m.startingInstance = nil

		if msg.err != nil {
			// Start failed — remove the exact instance and restore any filter.
			m.list.RemoveInstance(inst)
			m.untrackNewInstance(inst, true)
			return m, tea.Batch(tea.WindowSize(), m.instanceChanged(), m.handleError(msg.err))
		}

		// Started successfully — it is no longer the pending "new" instance.
		m.finishNewInstanceStarted(inst)

		// Save after successful start.
		if err := m.storage.SaveInstances(m.list.GetInstances()); err != nil {
			return m, m.handleError(err)
		}

		if m.promptAfterName {
			m.state = statePrompt
			m.menu.SetState(ui.StatePrompt)
			m.textInputOverlay = overlay.NewTextInputOverlay("Enter prompt", "")
			m.promptAfterName = false
		} else {
			m.showHelpScreen(helpStart(inst), nil)
		}

		return m, tea.Batch(tea.WindowSize(), m.instanceChanged())
	case sessionsLoadedMsg:
		if msg.err != nil {
			return m, m.handleError(msg.err)
		}
		m.sessionBrowser.SetSessions(msg.sessions)
		m.sessionBrowser.SetSkipped(msg.skipped)
		m.sessionBrowser.SetIndexing(true)
		// Build/refresh the content index off the UI thread. Until it is ready the
		// browser filters on metadata only.
		return m, buildCopilotIndexCmd(msg.sessions)
	case indexReadyMsg:
		m.sessionBrowser.SetIndexing(false)
		if msg.err != nil {
			log.WarningLog.Printf("copilot index: %v", msg.err)
		}
		if msg.index != nil {
			m.copilotIndex = msg.index
			idx := msg.index
			m.sessionBrowser.SetFilterFunc(func(sessions []copilot.Session, query string) []copilot.Session {
				return idx.Search(context.Background(), sessions, query)
			})
			// Re-apply the current query so results reflect the content index.
			m.sessionBrowser.SetQuery(m.sessionBrowser.Query())
		}
		return m, nil
	case metadataUpdateDoneMsg:
		// One timestamp for the whole batch so instances updated in the same tick
		// compare equal in "recent activity" ordering (anti-thrash, R2).
		batchNow := time.Now()
		for _, r := range msg.results {
			// Skip instances that were paused while metadata was being computed
			if r.instance.Status == session.Paused {
				continue
			}
			if r.updated {
				r.instance.SetStatus(session.Running)
				r.instance.NoteActivity(batchNow)
			} else if r.hasPrompt {
				r.instance.TryAutoApprove()
			} else {
				r.instance.SetStatus(session.Ready)
			}
			// Orthogonal side-effect: recompute the waiting-for-user (pending)
			// signal each tick (AutoYes-aware; excludes Paused/Loading). Does not
			// affect the Running/Ready transitions above.
			r.instance.RefreshWaitingForUser(r.updated, r.hasPrompt)
			if r.diffStats != nil && r.diffStats.Error != nil {
				if !strings.Contains(r.diffStats.Error.Error(), "base commit SHA not set") {
					log.WarningLog.Printf("could not update diff stats: %v", r.diffStats.Error)
				}
				r.instance.SetDiffStats(nil)
			} else {
				r.instance.SetDiffStats(r.diffStats)
			}
		}
		// Reflect any ordering changes (recent-activity / pinned-pending) into the
		// view and animate moved rows.
		m.list.Refresh()
		return m, tea.Batch(
			tickUpdateMetadataCmd(m.snapshotActiveInstances(), m.list.GetSelectedInstance()),
			m.scheduleAnimationIfNeeded(),
		)
	case animTickMsg:
		// Stale tick from a superseded animation loop: ignore (R4, single loop).
		if msg.gen != m.animGen {
			return m, nil
		}
		if m.list.StepAnimation() {
			return m, animTickCmd(m.animGen)
		}
		m.animating = false
		return m, nil
	case tea.MouseMsg:
		// Handle mouse wheel events for scrolling the diff/preview pane
		if msg.Action == tea.MouseActionPress {
			if msg.Button == tea.MouseButtonWheelDown || msg.Button == tea.MouseButtonWheelUp {
				selected := m.list.GetSelectedInstance()
				if selected == nil || selected.Status == session.Paused {
					return m, nil
				}

				switch msg.Button {
				case tea.MouseButtonWheelUp:
					m.tabbedWindow.ScrollUp()
				case tea.MouseButtonWheelDown:
					m.tabbedWindow.ScrollDown()
				}
			}
		}
		return m, nil
	case branchSearchDebounceMsg:
		// Debounce timer fired — check if this is still the current filter version
		if m.textInputOverlay == nil {
			return m, nil
		}
		if msg.version != m.textInputOverlay.BranchFilterVersion() {
			return m, nil // stale, a newer debounce is pending
		}
		return m, m.runBranchSearch(msg.filter, msg.version)
	case branchSearchResultMsg:
		if m.textInputOverlay != nil {
			m.textInputOverlay.SetBranchResults(msg.branches, msg.version)
		}
		return m, nil
	case tea.KeyMsg:
		model, cmd := m.handleKeyPress(msg)
		return model, tea.Batch(cmd, m.scheduleAnimationIfNeeded())
	case tea.WindowSizeMsg:
		m.updateHandleWindowSizeEvent(msg)
		return m, nil
	case error:
		// Handle errors from confirmation actions
		return m, m.handleError(msg)
	case instanceChangedMsg:
		// Handle instance changed after confirmation action
		return m, m.instanceChanged()
	case attachFinishedMsg:
		// Returned after a tea.Exec-based attach (native Windows) completes.
		// bubbletea has already released+restored the terminal (full repaint).
		m.state = stateDefault
		m.menu.SetState(ui.StateDefault)
		if msg.err != nil {
			return m, tea.Batch(m.handleError(msg.err), m.instanceChanged())
		}
		return m, tea.Batch(tea.WindowSize(), m.instanceChanged())
	case instanceStartedMsg:
		// Select the instance that just started (or failed)
		m.list.SelectInstance(msg.instance)

		if msg.err != nil {
			// If a resumed session failed to start, drop it from the resumed set so the
			// user can try again.
			if msg.instance != nil && msg.instance.AgentSessionID != "" {
				delete(m.resumedSessionIDs, msg.instance.AgentSessionID)
			}
			// Remove the exact instance that failed to start and restore any filter.
			m.list.RemoveInstance(msg.instance)
			m.untrackNewInstance(msg.instance, true)
			return m, tea.Batch(m.handleError(msg.err), m.instanceChanged())
		}

		// Started successfully — it is no longer the pending "new" instance.
		m.finishNewInstanceStarted(msg.instance)

		// Save after successful start
		if err := m.storage.SaveInstances(m.list.GetInstances()); err != nil {
			return m, m.handleError(err)
		}
		if m.autoYes {
			msg.instance.SetAutoYes(true)
		}

		if msg.promptAfterName {
			m.state = statePrompt
			m.menu.SetState(ui.StatePrompt)
			m.textInputOverlay = m.newPromptOverlay()
		} else {
			// If instance has a prompt (set from Shift+N flow), send it now
			if msg.instance.Prompt != "" {
				if err := msg.instance.SendPrompt(msg.instance.Prompt); err != nil {
					log.ErrorLog.Printf("failed to send prompt: %v", err)
				}
				msg.instance.Prompt = ""
			}
			m.menu.SetState(ui.StateDefault)
			m.showHelpScreen(helpStart(msg.instance), nil)
		}

		return m, tea.Batch(tea.WindowSize(), m.instanceChanged())
	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m *home) handleQuit() (tea.Model, tea.Cmd) {
	if err := m.storage.SaveInstances(m.list.GetInstances()); err != nil {
		return m, m.handleError(err)
	}
	return m, tea.Quit
}

func (m *home) handleMenuHighlighting(msg tea.KeyMsg) (cmd tea.Cmd, returnEarly bool) {
	// Handle menu highlighting when you press a button. We intercept it here and immediately return to
	// update the ui while re-sending the keypress. Then, on the next call to this, we actually handle the keypress.
	if m.keySent {
		m.keySent = false
		return nil, false
	}
	if m.state == statePrompt || m.state == stateHelp || m.state == stateConfirm || m.state == stateSearch || m.state == stateBrowse {
		return nil, false
	}
	// If it's in the global keymap, we should try to highlight it.
	name, ok := keys.GlobalKeyStringsMap[msg.String()]
	if !ok {
		return nil, false
	}

	if m.list.GetSelectedInstance() != nil && m.list.GetSelectedInstance().Paused() && name == keys.KeyEnter {
		return nil, false
	}
	if name == keys.KeyShiftDown || name == keys.KeyShiftUp {
		return nil, false
	}

	// Skip the menu highlighting if the key is not in the map or we are using the shift up and down keys.
	// TODO: cleanup: when you press enter on stateNew, we use keys.KeySubmitName. We should unify the keymap.
	if name == keys.KeyEnter && m.state == stateNew {
		name = keys.KeySubmitName
	}
	m.keySent = true
	return tea.Batch(
		func() tea.Msg { return msg },
		m.keydownCallback(name)), true
}

func (m *home) handleKeyPress(msg tea.KeyMsg) (mod tea.Model, cmd tea.Cmd) {
	cmd, returnEarly := m.handleMenuHighlighting(msg)
	if returnEarly {
		return m, cmd
	}

	if m.state == stateHelp {
		return m.handleHelpState(msg)
	}

	// Search input owns all keys while open; Esc here takes precedence over
	// preview/terminal scroll handling below.
	if m.state == stateSearch {
		return m.handleSearchKey(msg)
	}

	if m.state == stateNew {
		// Handle quit commands first. Don't handle q because the user might want to type that.
		if msg.String() == "ctrl+c" {
			m.state = stateDefault
			m.promptAfterName = false
			m.cancelNewInstance()
			return m, tea.Sequence(
				tea.WindowSize(),
				func() tea.Msg {
					m.menu.SetState(ui.StateDefault)
					return nil
				},
			)
		}

		instance := m.newInstance
		if instance == nil {
			// Defensive: stateNew without a tracked new instance — bail out.
			m.state = stateDefault
			return m, nil
		}
		switch msg.Type {
		// Start the instance (enable previews etc) and go back to the main menu state.
		case tea.KeyEnter:
			if len(instance.Title) == 0 {
				return m, m.handleError(fmt.Errorf("title cannot be empty"))
			}

			// If promptAfterName, show prompt+branch overlay before starting
			if m.promptAfterName {
				m.promptAfterName = false
				m.state = statePrompt
				m.menu.SetState(ui.StatePrompt)
				m.textInputOverlay = m.newPromptOverlay()
				// Trigger initial branch search (no debounce, version 0)
				initialSearch := m.runBranchSearch("", m.textInputOverlay.BranchFilterVersion())
				return m, tea.Batch(tea.WindowSize(), initialSearch)
			}

			// Set Loading status and finalize into the list immediately
			instance.SetStatus(session.Loading)
			m.newInstanceFinalizer()
			m.promptAfterName = false
			m.state = stateDefault
			m.menu.SetState(ui.StateDefault)

			// Return a tea.Cmd that runs instance.Start in the background
			startCmd := func() tea.Msg {
				err := instance.Start(true)
				return instanceStartedMsg{
					instance:        instance,
					err:             err,
					promptAfterName: false,
				}
			}

			return m, tea.Batch(tea.WindowSize(), m.instanceChanged(), startCmd)
		case tea.KeyRunes:
			if runewidth.StringWidth(instance.Title) >= 32 {
				return m, m.handleError(fmt.Errorf("title cannot be longer than 32 characters"))
			}
			if err := instance.SetTitle(instance.Title + string(msg.Runes)); err != nil {
				return m, m.handleError(err)
			}
		case tea.KeyBackspace:
			runes := []rune(instance.Title)
			if len(runes) == 0 {
				return m, nil
			}
			if err := instance.SetTitle(string(runes[:len(runes)-1])); err != nil {
				return m, m.handleError(err)
			}
		case tea.KeySpace:
			if err := instance.SetTitle(instance.Title + " "); err != nil {
				return m, m.handleError(err)
			}
		case tea.KeyEsc:
			m.cancelNewInstance()
			m.state = stateDefault
			m.instanceChanged()

			return m, tea.Sequence(
				tea.WindowSize(),
				func() tea.Msg {
					m.menu.SetState(ui.StateDefault)
					return nil
				},
			)
		default:
		}
		return m, nil
	} else if m.state == statePrompt {
		// Handle cancel via ctrl+c before delegating to the overlay
		if msg.String() == "ctrl+c" {
			return m, m.cancelPromptOverlay()
		}

		// Use the new TextInputOverlay component to handle all key events
		shouldClose, branchFilterChanged := m.textInputOverlay.HandleKeyPress(msg)

		// Check if the form was submitted or canceled
		if shouldClose {
			selected := m.list.GetSelectedInstance()
			if selected == nil {
				return m, nil
			}

			if m.textInputOverlay.IsCanceled() {
				return m, m.cancelPromptOverlay()
			}

			if m.textInputOverlay.IsSubmitted() {
				prompt := m.textInputOverlay.GetValue()
				selectedBranch := m.textInputOverlay.GetSelectedBranch()
				selectedProgram := m.textInputOverlay.GetSelectedProgram()

				if !selected.Started() {
					// Shift+N flow: instance not started yet — set branch, start, then send prompt
					if selectedBranch != "" {
						selected.SetSelectedBranch(selectedBranch)
					}
					if selectedProgram != "" {
						selected.Program = selectedProgram
					}
					selected.Prompt = prompt

					// Finalize into list and start
					selected.SetStatus(session.Loading)
					m.newInstanceFinalizer()
					m.textInputOverlay = nil
					m.state = stateDefault
					m.menu.SetState(ui.StateDefault)

					startCmd := func() tea.Msg {
						err := selected.Start(true)
						return instanceStartedMsg{
							instance:        selected,
							err:             err,
							promptAfterName: false,
							selectedBranch:  selectedBranch,
						}
					}

					return m, tea.Batch(tea.WindowSize(), m.instanceChanged(), startCmd)
				}

				// Regular flow: instance already running, just send prompt
				if err := selected.SendPrompt(prompt); err != nil {
					return m, m.handleError(err)
				}
			}

			// Close the overlay and reset state
			m.textInputOverlay = nil
			m.state = stateDefault
			return m, tea.Sequence(
				tea.WindowSize(),
				func() tea.Msg {
					m.menu.SetState(ui.StateDefault)
					m.showHelpScreen(helpStart(selected), nil)
					return nil
				},
			)
		}

		// Schedule a debounced branch search if the filter changed
		if branchFilterChanged {
			filter := m.textInputOverlay.BranchFilter()
			version := m.textInputOverlay.BranchFilterVersion()
			return m, m.scheduleBranchSearch(filter, version)
		}

		return m, nil
	}

	// Handle confirmation state
	if m.state == stateConfirm {
		shouldClose := m.confirmationOverlay.HandleKeyPress(msg)
		if shouldClose {
			m.state = stateDefault
			m.confirmationOverlay = nil
			return m, nil
		}
		return m, nil
	}

	if m.state == stateBrowse {
		if msg.String() == "ctrl+c" {
			m.state = stateDefault
			m.menu.SetState(ui.StateDefault)
			return m, tea.WindowSize()
		}
		if msg.String() == "ctrl+r" {
			// Force a full re-scan and index rebuild (ignore the cached index).
			if path, err := copilot.IndexPath(); err == nil {
				_ = os.Remove(path)
			}
			return m, m.loadCopilotSessionsCmd()
		}
		action, cmd := m.sessionBrowser.HandleKeyPress(msg)
		switch action {
		case ui.BrowserActionClose:
			m.state = stateDefault
			m.menu.SetState(ui.StateDefault)
			return m, tea.Batch(tea.WindowSize(), cmd)
		case ui.BrowserActionRestart:
			return m.handleSessionRestart(m.sessionBrowser.GetSelected())
		default:
			return m, cmd
		}
	}

	// Exit scrolling mode when ESC is pressed and preview pane is in scrolling mode
	// Check if Escape key was pressed and we're not in the diff tab (meaning we're in preview tab)
	// Always check for escape key first to ensure it doesn't get intercepted elsewhere
	if msg.Type == tea.KeyEsc {
		// If in preview tab and in scroll mode, exit scroll mode
		if m.tabbedWindow.IsInPreviewTab() && m.tabbedWindow.IsPreviewInScrollMode() {
			// Use the selected instance from the list
			selected := m.list.GetSelectedInstance()
			err := m.tabbedWindow.ResetPreviewToNormalMode(selected)
			if err != nil {
				return m, m.handleError(err)
			}
			return m, m.instanceChanged()
		}
		// If in terminal tab and in scroll mode, exit scroll mode
		if m.tabbedWindow.IsInTerminalTab() && m.tabbedWindow.IsTerminalInScrollMode() {
			m.tabbedWindow.ResetTerminalToNormalMode()
			return m, m.instanceChanged()
		}
	}

	// Handle quit commands first
	if msg.String() == "ctrl+c" || msg.String() == "q" {
		return m.handleQuit()
	}

	name, ok := keys.GlobalKeyStringsMap[msg.String()]
	if !ok {
		return m, nil
	}

	switch name {
	case keys.KeyHelp:
		return m.showHelpScreen(helpTypeGeneral{}, nil)
	case keys.KeySearch:
		return m.enterSearch()
	case keys.KeyBrowse:
		m.state = stateBrowse
		m.menu.SetState(ui.StateBrowse)
		m.sessionBrowser.SetQuery("")
		return m, tea.Batch(tea.WindowSize(), m.loadCopilotSessionsCmd())
	case keys.KeyPrompt:
		if m.list.NumInstances() >= GlobalInstanceLimit {
			return m, m.handleError(
				fmt.Errorf("you can't create more than %d instances", GlobalInstanceLimit))
		}

		// Start a background fetch so branches are up to date by the time the picker opens
		fetchCmd := func() tea.Msg {
			currentDir, _ := os.Getwd()
			git.FetchBranches(currentDir)
			return nil
		}

		instance, err := session.NewInstance(session.InstanceOptions{
			Title:   "",
			Path:    ".",
			Program: m.program,
		})
		if err != nil {
			return m, m.handleError(err)
		}

		m.beginNewInstance(instance)
		m.state = stateNew
		m.menu.SetState(ui.StateNewInstance)
		m.promptAfterName = true

		return m, fetchCmd
	case keys.KeyNew:
		if m.list.NumInstances() >= GlobalInstanceLimit {
			return m, m.handleError(
				fmt.Errorf("you can't create more than %d instances", GlobalInstanceLimit))
		}
		instance, err := session.NewInstance(session.InstanceOptions{
			Title:   "",
			Path:    ".",
			Program: m.program,
		})
		if err != nil {
			return m, m.handleError(err)
		}

		m.beginNewInstance(instance)
		m.state = stateNew
		m.menu.SetState(ui.StateNewInstance)

		return m, nil
	case keys.KeyUp:
		m.list.Up()
		return m, m.instanceChanged()
	case keys.KeyDown:
		m.list.Down()
		return m, m.instanceChanged()
	case keys.KeyShiftUp:
		m.tabbedWindow.ScrollUp()
		return m, m.instanceChanged()
	case keys.KeyShiftDown:
		m.tabbedWindow.ScrollDown()
		return m, m.instanceChanged()
	case keys.KeyTab:
		m.tabbedWindow.Toggle()
		m.menu.SetActiveTab(m.tabbedWindow.GetActiveTab())
		return m, m.instanceChanged()
	case keys.KeyKill:
		selected := m.list.GetSelectedInstance()
		if selected == nil || selected.Status == session.Loading {
			return m, nil
		}

		// Create the kill action as a tea.Cmd
		killAction := func() tea.Msg {
			// Get worktree and check if branch is checked out
			worktree, err := selected.GetGitWorktree()
			if err != nil {
				return err
			}

			checkedOut, err := worktree.IsBranchCheckedOut()
			if err != nil {
				return err
			}

			if checkedOut {
				return fmt.Errorf("instance %s is currently checked out", selected.Title)
			}

			// Clean up terminal session for this instance
			m.tabbedWindow.CleanupTerminalForInstance(selected.Title)

			// Delete from storage first
			if err := m.storage.DeleteInstance(selected.Title); err != nil {
				return err
			}

			// Then kill the selected instance's session and remove it.
			m.list.KillSelected()
			return instanceChangedMsg{}
		}

		// Show confirmation modal
		message := fmt.Sprintf("[!] Kill session '%s'?", selected.Title)
		return m, m.confirmAction(message, killAction)
	case keys.KeySubmit:
		selected := m.list.GetSelectedInstance()
		if selected == nil || selected.Status == session.Loading {
			return m, nil
		}

		// Create the push action as a tea.Cmd
		pushAction := func() tea.Msg {
			// Default commit message with timestamp
			commitMsg := fmt.Sprintf("[claudesquad] update from '%s' on %s", selected.Title, time.Now().Format(time.RFC822))
			worktree, err := selected.GetGitWorktree()
			if err != nil {
				return err
			}
			if err = worktree.PushChanges(commitMsg, true); err != nil {
				return err
			}
			return nil
		}

		// Show confirmation modal
		message := fmt.Sprintf("[!] Push changes from session '%s'?", selected.Title)
		return m, m.confirmAction(message, pushAction)
	case keys.KeyCheckout:
		selected := m.list.GetSelectedInstance()
		if selected == nil || selected.Status == session.Loading {
			return m, nil
		}

		// Show help screen before pausing
		m.showHelpScreen(helpTypeInstanceCheckout{}, func() {
			if err := selected.Pause(); err != nil {
				m.handleError(err)
			}
			m.tabbedWindow.CleanupTerminalForInstance(selected.Title)
			m.instanceChanged()
		})
		return m, nil
	case keys.KeyMoveUp:
		return m.reorderSelected(true)
	case keys.KeyMoveDown:
		return m.reorderSelected(false)
	case keys.KeyModeCycle:
		return m.cycleMode(true)
	case keys.KeyModeCycleBack:
		return m.cycleMode(false)
	case keys.KeyResume:
		selected := m.list.GetSelectedInstance()
		if selected == nil || selected.Status == session.Loading {
			return m, nil
		}
		if err := selected.Resume(); err != nil {
			return m, m.handleError(err)
		}
		return m, tea.WindowSize()
	case keys.KeyEnter:
		if m.list.NumInstances() == 0 {
			return m, nil
		}
		selected := m.list.GetSelectedInstance()
		if selected == nil || selected.Paused() || selected.Status == session.Loading || !selected.SessionAlive() {
			return m, nil
		}
		// Terminal tab: attach to terminal session
		if m.tabbedWindow.IsInTerminalTab() {
			m.showHelpScreen(helpTypeInstanceAttach{}, func() {
				ch, err := m.tabbedWindow.AttachTerminal()
				if err != nil {
					m.handleError(err)
					return
				}
				<-ch
				m.state = stateDefault
			})
			return m, nil
		}
		// Show help screen before attaching
		return m.startAttach(selected)
	default:
		return m, nil
	}
}

// reorderSelected moves the selected instance up or down in the canonical order.
// Reorder is only available in Manual mode; in any other mode it is a no-op with
// a transient hint (it never auto-switches mode, which would scramble the view).
func (m *home) reorderSelected(up bool) (tea.Model, tea.Cmd) {
	if m.list.Mode() != ui.ModeManual {
		return m, m.transientHint("reorder is only available in Manual mode")
	}
	var moved bool
	if up {
		moved = m.list.MoveSelectedUp()
	} else {
		moved = m.list.MoveSelectedDown()
	}
	if !moved {
		return m, nil
	}
	if err := m.storage.SaveInstances(m.list.GetInstances()); err != nil {
		return m, m.handleError(err)
	}
	return m, m.instanceChanged()
}

// cycleMode advances the sidebar view mode forward or backward and persists it.
func (m *home) cycleMode(forward bool) (tea.Model, tea.Cmd) {
	next := m.list.Mode().Next()
	if !forward {
		next = m.list.Mode().Prev()
	}
	m.list.SetMode(next)
	if err := m.appState.SetSidebarMode(int(next)); err != nil {
		return m, m.handleError(err)
	}
	return m, m.instanceChanged()
}

// transientHint shows a short advisory message in the status box and clears it
// after a delay. Unlike handleError it does not log to the error log.
func (m *home) transientHint(msg string) tea.Cmd {
	m.errBox.SetError(fmt.Errorf("%s", msg))
	return func() tea.Msg {
		select {
		case <-m.ctx.Done():
		case <-time.After(3 * time.Second):
		}
		return hideErrMsg{}
	}
}

// enterSearch opens the sidebar search input, capturing the current selection so
// Esc can restore it.
func (m *home) enterSearch() (tea.Model, tea.Cmd) {
	if m.list.NumInstances() == 0 {
		return m, nil
	}
	m.preSearchSelection = m.list.GetSelectedInstance()
	m.state = stateSearch
	m.list.SetSearching(true)
	m.menu.SetState(ui.StateSearch)
	return m, m.instanceChanged()
}

// handleSearchKey routes keys while the search input is open: letters/digits/
// space/backspace edit the query; only the arrow keys navigate (so j/k are text);
// Enter commits the filter and Esc clears it and restores the pre-search
// selection. All other global actions are suppressed.
func (m *home) handleSearchKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEsc, tea.KeyCtrlC:
		return m.exitSearch(true)
	case tea.KeyEnter:
		return m.exitSearch(false)
	case tea.KeyUp:
		m.list.Up()
		return m, m.instanceChanged()
	case tea.KeyDown:
		m.list.Down()
		return m, m.instanceChanged()
	case tea.KeyBackspace:
		runes := []rune(m.list.Filter())
		if len(runes) > 0 {
			m.list.SetFilter(string(runes[:len(runes)-1]))
			m.updateSearchSelection()
		}
		return m, m.instanceChanged()
	case tea.KeySpace:
		m.list.SetFilter(m.list.Filter() + " ")
		m.updateSearchSelection()
		return m, m.instanceChanged()
	case tea.KeyRunes:
		m.list.SetFilter(m.list.Filter() + string(msg.Runes))
		m.updateSearchSelection()
		return m, m.instanceChanged()
	default:
		return m, nil
	}
}

// exitSearch closes the search input. When restore is true (Esc), the filter is
// cleared and the pre-search selection is restored; otherwise (Enter) the filter
// is kept and the current visible selection retained.
func (m *home) exitSearch(restore bool) (tea.Model, tea.Cmd) {
	if restore {
		m.list.SetFilter("")
		if m.preSearchSelection != nil {
			m.list.SelectInstance(m.preSearchSelection)
		}
	} else if m.list.GetSelectedInstance() == nil {
		m.list.SelectFirstVisible()
	}
	m.preSearchSelection = nil
	m.list.SetSearching(false)
	m.state = stateDefault
	m.menu.SetState(ui.StateDefault)
	return m, m.instanceChanged()
}

// updateSearchSelection keeps the pre-search selection selected while it still
// matches the filter, otherwise selects the first visible match (R6).
func (m *home) updateSearchSelection() {
	if m.preSearchSelection != nil {
		m.list.SelectInstance(m.preSearchSelection)
		if m.list.GetSelectedInstance() != nil {
			return
		}
	}
	m.list.SelectFirstVisible()
}

// beginNewInstance adds a new unstarted instance, suspends any active search
// filter (so the new row is visible while naming), and selects it by identity.
func (m *home) beginNewInstance(instance *session.Instance) {
	m.preNewFilter = m.list.Filter()
	if m.preNewFilter != "" {
		m.list.SetFilter("")
	}
	m.newInstanceFinalizer = m.list.AddInstance(instance)
	m.newInstance = instance
	m.list.SelectNewInstance(instance)
}

// cancelNewInstance removes the in-creation instance (if any) by identity and
// restores any suspended search filter. Used for user-initiated cancels.
func (m *home) cancelNewInstance() {
	inst := m.newInstance
	if inst != nil {
		m.list.RemoveInstance(inst)
	}
	m.untrackNewInstance(inst, true)
}

// untrackNewInstance clears the new-instance bookkeeping, but only when it still
// refers to inst — so a second creation started during an async start window is
// not disturbed (identity-exact, R3). When restoreFilter is true any suspended
// search filter is restored (cancel/failure); on success it is left cleared so
// the new workspace stays visible.
func (m *home) untrackNewInstance(inst *session.Instance, restoreFilter bool) {
	if m.newInstance != inst {
		return
	}
	m.newInstance = nil
	if restoreFilter && m.preNewFilter != "" {
		m.list.SetFilter(m.preNewFilter)
	}
	m.preNewFilter = ""
}

// finishNewInstanceStarted clears the new-instance tracking after a successful
// start, leaving any suspended filter cleared so the new workspace stays visible.
func (m *home) finishNewInstanceStarted(inst *session.Instance) {
	m.untrackNewInstance(inst, false)
}

// instanceChanged updates the preview pane, menu, and diff pane based on the selected instance. It returns an error
// Cmd if there was any error.
func (m *home) instanceChanged() tea.Cmd {
	// selected may be nil
	selected := m.list.GetSelectedInstance()

	m.tabbedWindow.UpdateDiff(selected)
	m.tabbedWindow.SetInstance(selected)
	// Update menu with current instance
	m.menu.SetInstance(selected)

	// If there's no selected instance, we don't need to update the preview.
	if err := m.tabbedWindow.UpdatePreview(selected); err != nil {
		return m.handleError(err)
	}
	if err := m.tabbedWindow.UpdateTerminal(selected); err != nil {
		return m.handleError(err)
	}
	return nil
}

type keyupMsg struct{}

type sessionsLoadedMsg struct {
	sessions []copilot.Session
	skipped  int
	err      error
}

func (m *home) loadCopilotSessionsCmd() tea.Cmd {
	return func() tea.Msg {
		sessions, skipped, err := copilot.DiscoverWithStats()
		return sessionsLoadedMsg{sessions: sessions, skipped: skipped, err: err}
	}
}

// indexReadyMsg is delivered when the Copilot content index finishes building.
type indexReadyMsg struct {
	index *copilot.Index
	err   error
}

// buildCopilotIndexCmd opens and refreshes the on-disk content index off the UI
// thread (discovery + content scan are the expensive part; the resulting in-memory
// index makes per-keystroke search trivially fast).
func buildCopilotIndexCmd(sessions []copilot.Session) tea.Cmd {
	return func() tea.Msg {
		idx, err := copilot.OpenIndex()
		if err != nil {
			return indexReadyMsg{err: err}
		}
		if buildErr := idx.Build(context.Background(), sessions); buildErr != nil {
			return indexReadyMsg{index: idx, err: buildErr}
		}
		return indexReadyMsg{index: idx}
	}
}

// keydownCallback clears the menu option highlighting after 500ms.
func (m *home) keydownCallback(name keys.KeyName) tea.Cmd {
	m.menu.Keydown(name)
	return func() tea.Msg {
		select {
		case <-m.ctx.Done():
		case <-time.After(500 * time.Millisecond):
		}

		return keyupMsg{}
	}
}

// hideErrMsg implements tea.Msg and clears the error text from the screen.
type hideErrMsg struct{}

// animTickMsg drives the sidebar row animation. gen identifies the animation loop
// that scheduled it so stale ticks from a superseded loop can be ignored (R4).
type animTickMsg struct{ gen uint64 }

// animFrameInterval is the delay between sidebar animation frames.
const animFrameInterval = 60 * time.Millisecond

// animTickCmd schedules the next animation frame for the given generation.
func animTickCmd(gen uint64) tea.Cmd {
	return func() tea.Msg {
		time.Sleep(animFrameInterval)
		return animTickMsg{gen: gen}
	}
}

// scheduleAnimationIfNeeded starts the animation tick loop if rows are pulsing and
// no loop is already running. A fresh generation is allocated on each not-animating
// -> animating transition so exactly one loop is ever live; mid-animation changes
// retarget the existing animator without starting a second loop.
func (m *home) scheduleAnimationIfNeeded() tea.Cmd {
	if !m.list.IsAnimating() || m.animating {
		return nil
	}
	m.animating = true
	m.animGen++
	return animTickCmd(m.animGen)
}

// previewTickMsg implements tea.Msg and triggers a preview update
type previewTickMsg struct{}

type instanceChangedMsg struct{}

// attachFinishedMsg is delivered when a tea.Exec-based attach (native Windows)
// returns, i.e. the user detached or the agent exited.
type attachFinishedMsg struct{ err error }

type instanceStartedMsg struct {
	instance        *session.Instance
	err             error
	promptAfterName bool
	selectedBranch  string
}

// branchSearchDebounceMsg fires after the debounce interval to trigger a search.
type branchSearchDebounceMsg struct {
	filter  string
	version uint64
}

// branchSearchResultMsg carries search results back to Update.
type branchSearchResultMsg struct {
	branches []string
	version  uint64
}

const branchSearchDebounce = 150 * time.Millisecond

// scheduleBranchSearch returns a debounced tea.Cmd: sleeps, then triggers a search message.
func (m *home) scheduleBranchSearch(filter string, version uint64) tea.Cmd {
	return func() tea.Msg {
		time.Sleep(branchSearchDebounce)
		return branchSearchDebounceMsg{filter: filter, version: version}
	}
}

// runBranchSearch returns a tea.Cmd that performs the git search in the background.
func (m *home) runBranchSearch(filter string, version uint64) tea.Cmd {
	return func() tea.Msg {
		currentDir, _ := os.Getwd()
		branches, err := git.SearchBranches(currentDir, filter)
		if err != nil {
			log.WarningLog.Printf("branch search failed: %v", err)
			return nil
		}
		return branchSearchResultMsg{branches: branches, version: version}
	}
}

// instanceMetaResult holds the results of a single instance's metadata update,
// computed in a background goroutine.
type instanceMetaResult struct {
	instance  *session.Instance
	updated   bool
	hasPrompt bool
	diffStats *git.DiffStats
}

// metadataUpdateDoneMsg is sent when the background metadata update completes.
type metadataUpdateDoneMsg struct {
	results []instanceMetaResult
}

// instanceStartDoneMsg is sent when the background instance start completes.
type instanceStartDoneMsg struct {
	instance *session.Instance
	err      error
}

// runInstanceStartCmd returns a Cmd that performs the expensive instance.Start(true)
// in a background goroutine so the main event loop stays responsive.
func runInstanceStartCmd(instance *session.Instance) tea.Cmd {
	return func() tea.Msg {
		err := instance.Start(true)
		return instanceStartDoneMsg{instance: instance, err: err}
	}
}

// snapshotActiveInstances returns the currently active (started, not paused)
// instances. Called on the main thread so the filtering doesn't race with
// state mutations.
func (m *home) snapshotActiveInstances() []*session.Instance {
	var out []*session.Instance
	for _, inst := range m.list.GetInstances() {
		if inst.Started() && !inst.Paused() {
			out = append(out, inst)
		}
	}
	return out
}

// tickUpdateMetadataCmd returns a self-chaining Cmd that sleeps 500ms, then performs
// expensive metadata I/O (tmux capture, git diff) in parallel background goroutines.
// Because it only re-schedules after completing, overlapping ticks are impossible.
// The active instances slice should be snapshotted on the main thread via
// snapshotActiveInstances() before being passed here.
//
// Only the selected instance gets a full diff (with Content); the rest get a
// lightweight numstat-only summary. This keeps per-instance memory bounded
// since the diff pane only ever renders the selected one.
func tickUpdateMetadataCmd(active []*session.Instance, selected *session.Instance) tea.Cmd {
	return func() tea.Msg {
		time.Sleep(500 * time.Millisecond)

		if len(active) == 0 {
			return metadataUpdateDoneMsg{}
		}

		results := make([]instanceMetaResult, len(active))
		var wg sync.WaitGroup
		for idx, inst := range active {
			wg.Add(1)
			go func(i int, instance *session.Instance) {
				defer wg.Done()
				r := &results[i]
				r.instance = instance
				r.updated, r.hasPrompt = instance.HasUpdated()
				if instance == selected {
					r.diffStats = instance.ComputeDiff()
				} else {
					r.diffStats = instance.ComputeDiffNumstat()
				}
			}(idx, inst)
		}
		wg.Wait()

		return metadataUpdateDoneMsg{results: results}
	}
}

// handleError handles all errors which get bubbled up to the app. sets the error message. We return a callback tea.Cmd that returns a hideErrMsg message
// which clears the error message after 3 seconds.
func (m *home) handleError(err error) tea.Cmd {
	log.ErrorLog.Printf("%v", err)
	m.errBox.SetError(err)
	return func() tea.Msg {
		select {
		case <-m.ctx.Done():
		case <-time.After(3 * time.Second):
		}

		return hideErrMsg{}
	}
}

func (m *home) newPromptOverlay() *overlay.TextInputOverlay {
	return overlay.NewTextInputOverlayWithBranchPicker("Enter prompt", "", m.appConfig.GetProfiles())
}

// handleSessionRestart resumes the selected Copilot session as a new, isolated
// claude-squad workspace launched with `copilot --resume=<id>`. The new worktree is
// created in the session's FROZEN origin repo on a new, uniquely-named branch based on
// the session's recorded HEAD; the session's original branch is never reused, checked
// out, or deleted. Resuming a session that is in use, or already resumed in this TUI,
// is blocked; resuming into a different repo asks for confirmation first.
func (m *home) handleSessionRestart(sel *copilot.Session) (tea.Model, tea.Cmd) {
	if sel == nil {
		return m, nil
	}

	if m.list.NumInstances() >= GlobalInstanceLimit {
		return m, m.handleError(fmt.Errorf("you can't create more than %d instances", GlobalInstanceLimit))
	}

	// Guard: already resumed in this TUI — select the existing workspace rather than
	// opening a second writer to the same session folder.
	if m.resumedSessionIDs[sel.ID] {
		for _, inst := range m.list.GetInstances() {
			if inst.AgentSessionID == sel.ID {
				m.list.SelectInstance(inst)
				break
			}
		}
		m.state = stateDefault
		m.menu.SetState(ui.StateDefault)
		return m, tea.Batch(tea.WindowSize(), m.instanceChanged(),
			m.handleError(fmt.Errorf("session %q is already open in this session", ui.SafeDisplay(sel.DisplayName()))))
	}

	// Guard: a session that is actively in use must not be resumed — two writers
	// would corrupt events.jsonl. Re-check the lock live, since the discovery-time
	// flag may be stale by now.
	if sel.InUse || copilot.IsInUse(sel.Dir) {
		return m, m.handleError(fmt.Errorf("session %q is currently in use; close it before resuming", ui.SafeDisplay(sel.DisplayName())))
	}

	// Resolve the target repo from the FROZEN origin, falling back to the current repo
	// (never the likely-deleted cwd) when the origin repo is missing on disk.
	targetRepo := sel.OriginRoot
	originMissing := false
	if targetRepo == "" {
		originMissing = true
	} else if info, err := os.Stat(targetRepo); err != nil || !info.IsDir() {
		originMissing = true
	}
	if originMissing {
		targetRepo = "."
	}

	// Compare against the current repo (best-effort path comparison) so we can confirm
	// before creating a worktree in a different repository.
	absTarget, _ := filepath.Abs(targetRepo)
	currentDir, _ := os.Getwd()
	absCurrent, _ := filepath.Abs(currentDir)
	crossRepo := !originMissing && !strings.EqualFold(filepath.Clean(absTarget), filepath.Clean(absCurrent))

	if crossRepo {
		message := fmt.Sprintf("Resume in %q? A new worktree/branch will be created there.", ui.SafeDisplay(absTarget))
		// The confirmation callback runs synchronously and discards returned cmds, so
		// performResume starts the instance synchronously on confirm.
		return m, m.confirmAction(message, func() tea.Msg {
			m.performResume(sel, targetRepo, false, "")
			return nil
		})
	}

	warn := ""
	if originMissing && sel.OriginRoot != "" {
		warn = fmt.Sprintf("origin repo %q not found; resuming in the current repo (file context may not match)", ui.SafeDisplay(sel.OriginRoot))
	}
	return m.performResume(sel, targetRepo, true, warn)
}

// buildResumeInstance constructs (but does not start) the Instance that resumes sel.
// The title carries a short id suffix so resuming distinct sessions — or the same
// session after a restart — never collides on the host session name or branch.
func (m *home) buildResumeInstance(sel *copilot.Session, targetRepo string) (*session.Instance, error) {
	idSuffix := sel.ID
	if len(idSuffix) > 6 {
		idSuffix = idSuffix[:6]
	}
	name := ui.SafeDisplay(sel.DisplayName())
	if r := []rune(name); len(r) > 20 {
		name = strings.TrimSpace(string(r[:20]))
	}
	title := fmt.Sprintf("%s (resume %s)", name, idSuffix)

	// Resuming a Copilot session requires a copilot launch command. If the configured
	// agent isn't copilot, fall back to plain "copilot" so --resume is honored.
	program := m.program
	if !agentcmd.SupportsResume(program) {
		program = "copilot"
	}

	inst, err := session.NewInstance(session.InstanceOptions{
		Title:   title,
		Path:    targetRepo,
		Program: program,
	})
	if err != nil {
		return nil, err
	}
	// Marks this instance as a RESUME (-> launch with --resume=<id>) and bases the new
	// branch on the session's recorded origin HEAD.
	inst.AgentSessionID = sel.ID
	inst.BaseCommit = sel.OriginHead
	return inst, nil
}

// performResume registers the resumed instance in the list and starts it. When async
// is true (same-repo path) the start runs in the background and completion is delivered
// via instanceStartedMsg; when false (the confirmed cross-repo path, whose returned cmd
// is discarded by the confirmation overlay) the start runs synchronously and is
// persisted here.
func (m *home) performResume(sel *copilot.Session, targetRepo string, async bool, warn string) (tea.Model, tea.Cmd) {
	inst, err := m.buildResumeInstance(sel, targetRepo)
	if err != nil {
		return m, m.handleError(err)
	}

	finalize := m.list.AddInstance(inst)
	m.list.SetSelectedInstance(m.list.NumInstances() - 1)
	inst.SetStatus(session.Loading)
	finalize()
	m.resumedSessionIDs[sel.ID] = true
	m.state = stateDefault
	m.menu.SetState(ui.StateDefault)

	if !async {
		if startErr := inst.Start(true); startErr != nil {
			m.list.RemoveInstance(inst)
			delete(m.resumedSessionIDs, sel.ID)
			return m, m.handleError(startErr)
		}
		m.list.SelectInstance(inst)
		if saveErr := m.storage.SaveInstances(m.list.GetInstances()); saveErr != nil {
			return m, m.handleError(saveErr)
		}
		return m, tea.Batch(tea.WindowSize(), m.instanceChanged())
	}

	startCmd := func() tea.Msg {
		startErr := inst.Start(true)
		return instanceStartedMsg{instance: inst, err: startErr}
	}
	cmds := []tea.Cmd{tea.WindowSize(), m.instanceChanged(), startCmd}
	if warn != "" {
		cmds = append(cmds, m.handleError(fmt.Errorf("%s", warn)))
	}
	return m, tea.Batch(cmds...)
}

// cancelPromptOverlay cancels the prompt overlay, cleaning up the unstarted
// new instance (if any) by identity and restoring any suspended filter.
func (m *home) cancelPromptOverlay() tea.Cmd {
	m.cancelNewInstance()
	m.textInputOverlay = nil
	m.state = stateDefault
	return tea.Sequence(
		tea.WindowSize(),
		func() tea.Msg {
			m.menu.SetState(ui.StateDefault)
			return nil
		},
	)
}

// confirmAction shows a confirmation modal and stores the action to execute on confirm
func (m *home) confirmAction(message string, action tea.Cmd) tea.Cmd {
	m.state = stateConfirm

	// Create and show the confirmation overlay using ConfirmationOverlay
	m.confirmationOverlay = overlay.NewConfirmationOverlay(message)
	// Set a fixed width for consistent appearance
	m.confirmationOverlay.SetWidth(50)

	// Set callbacks for confirmation and cancellation
	m.confirmationOverlay.OnConfirm = func() {
		m.state = stateDefault
		// Execute the action if it exists
		if action != nil {
			_ = action()
		}
	}

	m.confirmationOverlay.OnCancel = func() {
		m.state = stateDefault
	}

	return nil
}

func (m *home) View() string {
	listWithPadding := lipgloss.NewStyle().PaddingTop(1).Render(m.list.String())
	previewWithPadding := lipgloss.NewStyle().PaddingTop(1).Render(m.tabbedWindow.String())
	listAndPreview := lipgloss.JoinHorizontal(lipgloss.Top, listWithPadding, previewWithPadding)

	mainView := lipgloss.JoinVertical(
		lipgloss.Center,
		listAndPreview,
		m.menu.String(),
		m.errBox.String(),
	)

	if m.state == statePrompt {
		if m.textInputOverlay == nil {
			log.ErrorLog.Printf("text input overlay is nil")
		}
		return overlay.PlaceOverlay(0, 0, m.textInputOverlay.Render(), mainView, true, true)
	} else if m.state == stateHelp {
		if m.textOverlay == nil {
			log.ErrorLog.Printf("text overlay is nil")
		}
		return overlay.PlaceOverlay(0, 0, m.textOverlay.Render(), mainView, true, true)
	} else if m.state == stateConfirm {
		if m.confirmationOverlay == nil {
			log.ErrorLog.Printf("confirmation overlay is nil")
		}
		return overlay.PlaceOverlay(0, 0, m.confirmationOverlay.Render(), mainView, true, true)
	} else if m.state == stateBrowse {
		browserView := lipgloss.NewStyle().PaddingTop(1).Render(m.sessionBrowser.String())
		return lipgloss.JoinVertical(lipgloss.Center, browserView, m.menu.String(), m.errBox.String())
	}

	return mainView
}
