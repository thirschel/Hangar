package session

import (
	"claude-squad/log"
	"claude-squad/session/git"
	"claude-squad/session/winhost"
	"path/filepath"

	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/atotto/clipboard"
)

type Status int

const (
	// Running is the status when the instance is running and the agent is working.
	Running Status = iota
	// Ready is if the agent instance is ready to be interacted with (waiting for user input).
	Ready
	// Loading is if the instance is loading (if we are starting it up or something).
	Loading
	// Paused is if the instance is paused (worktree removed but branch preserved).
	Paused
)

// Instance is a running instance of claude code.
type Instance struct {
	// Title is the title of the instance.
	Title string
	// Path is the path to the workspace.
	Path string
	// Branch is the branch of the instance.
	Branch string
	// Status is the status of the instance.
	Status Status
	// Program is the program to run in the instance.
	Program string
	// Height is the height of the instance.
	Height int
	// Width is the width of the instance.
	Width int
	// CreatedAt is the time the instance was created.
	CreatedAt time.Time
	// UpdatedAt is the time the instance was last updated.
	UpdatedAt time.Time
	// LastActivityAt is the time of the last observed screen change (set from the
	// metadata tick batch timestamp, subject to the anti-thrash dwell). It powers
	// the "Recent activity" sidebar mode. Falls back to UpdatedAt/CreatedAt when zero.
	LastActivityAt time.Time
	// AutoYes is true if the instance should automatically press enter when prompted.
	AutoYes bool
	// Prompt is the initial prompt to pass to the instance on startup
	Prompt string

	// DiffStats stores the current git diff statistics
	diffStats *git.DiffStats

	// selectedBranch is the existing branch to start on (empty = new branch from HEAD)
	selectedBranch string

	// The below fields are initialized upon calling Start().

	started bool
	// waitingForUser is true when the agent is awaiting human input that will not
	// be auto-supplied (see RefreshWaitingForUser). It powers the "Pinned-pending"
	// sidebar mode. Recompute-only (not persisted) in v1.
	waitingForUser bool
	// termSession is the terminal session for the instance (tmux on Unix, Windows Terminal on Windows).
	termSession TerminalSession
	// gitWorktree is the git worktree for the instance.
	gitWorktree *git.GitWorktree
}

// recentActivityDwell is the minimum interval between LastActivityAt advances for
// a single instance. It is the "minimum dwell" half of the recent-activity
// anti-thrash rule: a continuously-streaming agent advances its activity stamp at
// most once per dwell, so co-streaming agents don't swap sidebar slots every tick.
// Tunable; resolved against real multi-agent usage.
const recentActivityDwell = 2 * time.Second

// ToInstanceData converts an Instance to its serializable form
func (i *Instance) ToInstanceData() InstanceData {
	data := InstanceData{
		Title:          i.Title,
		Path:           i.Path,
		Branch:         i.Branch,
		Status:         i.Status,
		Height:         i.Height,
		Width:          i.Width,
		CreatedAt:      i.CreatedAt,
		UpdatedAt:      time.Now(),
		LastActivityAt: i.LastActivityAt,
		Program:        i.Program,
		AutoYes:        i.AutoYes,
	}

	// Only include worktree data if gitWorktree is initialized
	if i.gitWorktree != nil {
		data.Worktree = GitWorktreeData{
			RepoPath:         i.gitWorktree.GetRepoPath(),
			WorktreePath:     i.gitWorktree.GetWorktreePath(),
			SessionName:      i.Title,
			BranchName:       i.gitWorktree.GetBranchName(),
			BaseCommitSHA:    i.gitWorktree.GetBaseCommitSHA(),
			IsExistingBranch: i.gitWorktree.IsExistingBranch(),
		}
	}

	// Only include diff stats if they exist
	if i.diffStats != nil {
		data.DiffStats = DiffStatsData{
			Added:   i.diffStats.Added,
			Removed: i.diffStats.Removed,
			Content: i.diffStats.Content,
		}
	}

	return data
}

// FromInstanceData creates a new Instance from serialized data
func FromInstanceData(data InstanceData) (*Instance, error) {
	instance := &Instance{
		Title:          data.Title,
		Path:           data.Path,
		Branch:         data.Branch,
		Status:         data.Status,
		Height:         data.Height,
		Width:          data.Width,
		CreatedAt:      data.CreatedAt,
		UpdatedAt:      data.UpdatedAt,
		LastActivityAt: data.LastActivityAt,
		Program:        data.Program,
		gitWorktree: git.NewGitWorktreeFromStorage(
			data.Worktree.RepoPath,
			data.Worktree.WorktreePath,
			data.Worktree.SessionName,
			data.Worktree.BranchName,
			data.Worktree.BaseCommitSHA,
			data.Worktree.IsExistingBranch,
		),
		diffStats: &git.DiffStats{
			Added:   data.DiffStats.Added,
			Removed: data.DiffStats.Removed,
			Content: data.DiffStats.Content,
		},
	}

	if instance.Paused() {
		instance.started = true
		instance.termSession = NewTerminalSession(instance.Title, instance.Program)
	} else {
		if err := instance.Start(false); err != nil {
			return nil, err
		}
	}

	return instance, nil
}

// Options for creating a new instance
type InstanceOptions struct {
	// Title is the title of the instance.
	Title string
	// Path is the path to the workspace.
	Path string
	// Program is the program to run in the instance (e.g. "claude", "aider --model ollama_chat/gemma3:1b")
	Program string
	// If AutoYes is true, then
	AutoYes bool
	// Branch is an existing branch name to start the session on (empty = new branch from HEAD)
	Branch string
}

func NewInstance(opts InstanceOptions) (*Instance, error) {
	t := time.Now()

	// Convert path to absolute
	absPath, err := filepath.Abs(opts.Path)
	if err != nil {
		return nil, fmt.Errorf("failed to get absolute path: %w", err)
	}

	return &Instance{
		Title:          opts.Title,
		Status:         Ready,
		Path:           absPath,
		Program:        opts.Program,
		Height:         0,
		Width:          0,
		CreatedAt:      t,
		UpdatedAt:      t,
		AutoYes:        false,
		selectedBranch: opts.Branch,
	}, nil
}

func (i *Instance) RepoName() (string, error) {
	if !i.started {
		return "", fmt.Errorf("cannot get repo name for instance that has not been started")
	}
	return i.gitWorktree.GetRepoName(), nil
}

// RepoPath returns the repository root path backing the instance's worktree. It
// is the grouping key for the Group-by-repo sidebar mode (keying on path, not
// name, avoids merging distinct repos that share a basename).
func (i *Instance) RepoPath() (string, error) {
	if !i.started {
		return "", fmt.Errorf("cannot get repo path for instance that has not been started")
	}
	return i.gitWorktree.GetRepoPath(), nil
}

func (i *Instance) SetStatus(status Status) {
	i.Status = status
	// A Paused or Loading instance is never waiting on the user. Clearing here
	// covers the case where the metadata tick skips Paused instances and would
	// otherwise leave a stale pending flag.
	if status == Paused || status == Loading {
		i.waitingForUser = false
	}
}

// SetSelectedBranch sets the branch to use when starting the instance.
func (i *Instance) SetSelectedBranch(branch string) {
	i.selectedBranch = branch
}

// firstTimeSetup is true if this is a new instance. Otherwise, it's one loaded from storage.
func (i *Instance) Start(firstTimeSetup bool) error {
	if i.Title == "" {
		return fmt.Errorf("instance title cannot be empty")
	}

	var termSession TerminalSession
	if i.termSession != nil {
		// Use existing terminal session (useful for testing)
		termSession = i.termSession
	} else {
		// Create new terminal session
		termSession = NewTerminalSession(i.Title, i.Program)
	}
	i.termSession = termSession

	if firstTimeSetup {
		if i.selectedBranch != "" {
			gitWorktree, err := git.NewGitWorktreeFromBranch(i.Path, i.selectedBranch, i.Title)
			if err != nil {
				return fmt.Errorf("failed to create git worktree from branch: %w", err)
			}
			i.gitWorktree = gitWorktree
			i.Branch = i.selectedBranch
		} else {
			gitWorktree, branchName, err := git.NewGitWorktree(i.Path, i.Title)
			if err != nil {
				return fmt.Errorf("failed to create git worktree: %w", err)
			}
			i.gitWorktree = gitWorktree
			i.Branch = branchName
		}
	}

	// Setup error handler to cleanup resources on any error
	var setupErr error
	defer func() {
		if setupErr != nil {
			if cleanupErr := i.Kill(); cleanupErr != nil {
				setupErr = fmt.Errorf("%v (cleanup error: %v)", setupErr, cleanupErr)
			}
		} else {
			i.started = true
		}
	}()

	if !firstTimeSetup {
		// Reuse existing session
		if err := termSession.Restore(); err != nil {
			// On the native-Windows host model the session may be gone (e.g. the
			// host died across a reboot). In that case recreate it in the existing
			// worktree rather than failing startup. tmux never returns
			// ErrSessionGone, so its behaviour is unchanged.
			if errors.Is(err, winhost.ErrSessionGone) {
				if startErr := termSession.Start(i.gitWorktree.GetWorktreePath()); startErr != nil {
					setupErr = fmt.Errorf("failed to recreate session: %w", startErr)
					return setupErr
				}
			} else {
				setupErr = fmt.Errorf("failed to restore existing session: %w", err)
				return setupErr
			}
		}
	} else {
		// Setup git worktree first
		if err := i.gitWorktree.Setup(); err != nil {
			setupErr = fmt.Errorf("failed to setup git worktree: %w", err)
			return setupErr
		}

		// Create new session
		if err := i.termSession.Start(i.gitWorktree.GetWorktreePath()); err != nil {
			// Cleanup git worktree if session creation fails
			if cleanupErr := i.gitWorktree.Cleanup(); cleanupErr != nil {
				err = fmt.Errorf("%v (cleanup error: %v)", err, cleanupErr)
			}
			setupErr = fmt.Errorf("failed to start new session: %w", err)
			return setupErr
		}
	}

	i.SetStatus(Running)

	// Propagate AutoYes to the backend in case it was set before the session
	// existed (e.g. restored instances, or the -y flag). On Windows this enables
	// host-side AutoYes; on Unix it is a no-op.
	if i.AutoYes {
		if err := i.termSession.SetAutoYes(true); err != nil {
			log.ErrorLog.Printf("error propagating auto-yes on start: %v", err)
		}
	}

	return nil
}

// Kill terminates the instance and cleans up all resources
func (i *Instance) Kill() error {
	if !i.started {
		// If instance was never started, just return success
		return nil
	}

	var errs []error

	// Always try to cleanup both resources, even if one fails
	// Clean up terminal session first since it's using the git worktree
	if i.termSession != nil {
		if err := i.termSession.Close(); err != nil {
			errs = append(errs, fmt.Errorf("failed to close terminal session: %w", err))
		}
	}

	// Then clean up git worktree
	if i.gitWorktree != nil {
		if err := i.gitWorktree.Cleanup(); err != nil {
			errs = append(errs, fmt.Errorf("failed to cleanup git worktree: %w", err))
		}
	}

	return i.combineErrors(errs)
}

// combineErrors combines multiple errors into a single error
func (i *Instance) combineErrors(errs []error) error {
	if len(errs) == 0 {
		return nil
	}
	if len(errs) == 1 {
		return errs[0]
	}

	errMsg := "multiple cleanup errors occurred:"
	for _, err := range errs {
		errMsg += "\n  - " + err.Error()
	}
	return fmt.Errorf("%s", errMsg)
}

func (i *Instance) Preview() (string, error) {
	if !i.started || i.Status == Paused {
		return "", nil
	}
	return i.termSession.CapturePaneContent()
}

func (i *Instance) HasUpdated() (updated bool, hasPrompt bool) {
	if !i.started {
		return false, false
	}
	return i.termSession.HasUpdated()
}

// NoteActivity records that a screen change was observed at batchNow. To damp
// sidebar reordering ("recent activity" mode), the stored LastActivityAt only
// advances when at least recentActivityDwell has elapsed since the last advance.
// batchNow should be a single timestamp shared across one metadata tick so that
// instances updated in the same tick compare equal (stable order within a tick).
func (i *Instance) NoteActivity(batchNow time.Time) {
	if i.LastActivityAt.IsZero() || batchNow.Sub(i.LastActivityAt) >= recentActivityDwell {
		i.LastActivityAt = batchNow
	}
}

// EffectiveActivityTime returns the timestamp used to order the instance in the
// "recent activity" sidebar mode: LastActivityAt when set, else UpdatedAt, else
// CreatedAt. This keeps instances restored from older state (no LastActivityAt)
// sortable.
func (i *Instance) EffectiveActivityTime() time.Time {
	if !i.LastActivityAt.IsZero() {
		return i.LastActivityAt
	}
	if !i.UpdatedAt.IsZero() {
		return i.UpdatedAt
	}
	return i.CreatedAt
}

// IsWaitingForUser reports whether the agent is awaiting human input that will
// not be auto-supplied. It powers the "Pinned-pending" sidebar mode. The value is
// derived each metadata tick by RefreshWaitingForUser; it is never raw hasPrompt.
func (i *Instance) IsWaitingForUser() bool {
	return i.waitingForUser
}

// RefreshWaitingForUser recomputes the waiting-for-user (pending) signal from the
// latest metadata tick. A workspace is pending iff it is started, not Paused, not
// Loading, currently shows a prompt (hasPrompt) whose screen is not actively
// changing (!updated), and AutoYes is off (so the prompt will not be auto-resolved
// by the TUI or the Windows session host). Paused/Loading and AutoYes-resolved
// prompts are explicitly excluded.
func (i *Instance) RefreshWaitingForUser(updated, hasPrompt bool) {
	i.waitingForUser = i.started &&
		i.Status != Paused &&
		i.Status != Loading &&
		hasPrompt &&
		!updated &&
		!i.AutoYes
}

// CheckAndHandleTrustPrompt checks for and dismisses the trust prompt for supported programs.
func (i *Instance) CheckAndHandleTrustPrompt() bool {
	if !i.started || i.termSession == nil {
		return false
	}
	program := i.Program
	if !strings.HasSuffix(program, ProgramClaude) &&
		!strings.HasSuffix(program, ProgramAider) &&
		!strings.HasSuffix(program, ProgramGemini) {
		return false
	}
	return i.termSession.CheckAndHandleTrustPrompt()
}

// TapEnter sends an enter key press to the terminal session if AutoYes is enabled.
func (i *Instance) TapEnter() {
	if !i.started || !i.AutoYes {
		return
	}
	if err := i.termSession.TapEnter(); err != nil {
		log.ErrorLog.Printf("error tapping enter: %v", err)
	}
}

// SetAutoYes sets the AutoYes flag and propagates it to the terminal backend.
// On Windows this hands AutoYes ownership to the session host (so prompts are
// auto-approved even when the TUI is closed, and paused while attached); on
// Unix the backend call is a no-op and AutoYes stays TUI/daemon-driven.
func (i *Instance) SetAutoYes(enabled bool) {
	i.AutoYes = enabled
	if i.started && i.termSession != nil {
		if err := i.termSession.SetAutoYes(enabled); err != nil {
			log.ErrorLog.Printf("error setting auto-yes: %v", err)
		}
	}
}

func (i *Instance) Attach() (chan struct{}, error) {
	if !i.started {
		return nil, fmt.Errorf("cannot attach instance that has not been started")
	}
	return i.termSession.Attach()
}

func (i *Instance) SetPreviewSize(width, height int) error {
	if !i.started || i.Status == Paused {
		return fmt.Errorf("cannot set preview size for instance that has not been started or " +
			"is paused")
	}
	return i.termSession.SetDetachedSize(width, height)
}

// GetGitWorktree returns the git worktree for the instance
func (i *Instance) GetGitWorktree() (*git.GitWorktree, error) {
	if !i.started {
		return nil, fmt.Errorf("cannot get git worktree for instance that has not been started")
	}
	return i.gitWorktree, nil
}

// GetWorktreePath returns the worktree path for the instance, or empty string if unavailable
func (i *Instance) GetWorktreePath() string {
	if i.gitWorktree == nil {
		return ""
	}
	return i.gitWorktree.GetWorktreePath()
}

func (i *Instance) Started() bool {
	return i.started
}

// SetTitle sets the title of the instance. Returns an error if the instance has started.
// We cant change the title once it's been used for a terminal session etc.
func (i *Instance) SetTitle(title string) error {
	if i.started {
		return fmt.Errorf("cannot change title of a started instance")
	}
	i.Title = title
	return nil
}

func (i *Instance) Paused() bool {
	return i.Status == Paused
}

// SessionAlive returns true if the terminal session is alive. This is a sanity check before attaching.
func (i *Instance) SessionAlive() bool {
	return i.termSession.DoesSessionExist()
}

// Pause stops the terminal session and removes the worktree, preserving the branch
func (i *Instance) Pause() error {
	if !i.started {
		return fmt.Errorf("cannot pause instance that has not been started")
	}
	if i.Status == Paused {
		return fmt.Errorf("instance is already paused")
	}

	var errs []error

	// If the worktree is orphaned (path or .git missing), git cannot operate
	// on it. Skip dirty check and Remove, prune any lingering metadata, then
	// transition to Paused so the user can recover via Resume.
	if valid, err := i.gitWorktree.IsValidWorktree(); err != nil {
		errs = append(errs, fmt.Errorf("failed to validate worktree: %w", err))
		log.ErrorLog.Print(err)
	} else if !valid {
		log.WarningLog.Printf("worktree at %s is orphaned; skipping dirty check and remove",
			i.gitWorktree.GetWorktreePath())
		if err := i.termSession.DetachSafely(); err != nil {
			errs = append(errs, fmt.Errorf("failed to detach terminal session: %w", err))
			log.ErrorLog.Print(err)
		}
		// Drop any leftover directory so a future Resume's `git worktree add` won't conflict.
		if err := os.RemoveAll(i.gitWorktree.GetWorktreePath()); err != nil {
			errs = append(errs, fmt.Errorf("failed to remove orphaned worktree directory: %w", err))
			log.ErrorLog.Print(err)
		}
		if err := i.gitWorktree.Prune(); err != nil {
			errs = append(errs, fmt.Errorf("failed to prune git worktrees: %w", err))
			log.ErrorLog.Print(err)
		}
		i.SetStatus(Paused)
		_ = clipboard.WriteAll(i.gitWorktree.GetBranchName())
		return i.combineErrors(errs)
	}

	// Check if there are any changes to commit
	if dirty, err := i.gitWorktree.IsDirty(); err != nil {
		errs = append(errs, fmt.Errorf("failed to check if worktree is dirty: %w", err))
		log.ErrorLog.Print(err)
	} else if dirty {
		// Commit changes locally (without pushing to GitHub)
		commitMsg := fmt.Sprintf("[claudesquad] update from '%s' on %s (paused)", i.Title, time.Now().Format(time.RFC822))
		if err := i.gitWorktree.CommitChanges(commitMsg); err != nil {
			errs = append(errs, fmt.Errorf("failed to commit changes: %w", err))
			log.ErrorLog.Print(err)
			// Return early if we can't commit changes to avoid corrupted state
			return i.combineErrors(errs)
		}
	}

	// Detach from terminal session instead of closing to preserve session output
	if err := i.termSession.DetachSafely(); err != nil {
		errs = append(errs, fmt.Errorf("failed to detach terminal session: %w", err))
		log.ErrorLog.Print(err)
		// Continue with pause process even if detach fails
	}

	// Check if worktree exists before trying to remove it
	if _, err := os.Stat(i.gitWorktree.GetWorktreePath()); err == nil {
		// Remove worktree but keep branch
		if err := i.gitWorktree.Remove(); err != nil {
			errs = append(errs, fmt.Errorf("failed to remove git worktree: %w", err))
			log.ErrorLog.Print(err)
			return i.combineErrors(errs)
		}

		// Only prune if remove was successful
		if err := i.gitWorktree.Prune(); err != nil {
			errs = append(errs, fmt.Errorf("failed to prune git worktrees: %w", err))
			log.ErrorLog.Print(err)
			return i.combineErrors(errs)
		}
	}

	i.SetStatus(Paused)
	_ = clipboard.WriteAll(i.gitWorktree.GetBranchName())

	if err := i.combineErrors(errs); err != nil {
		log.ErrorLog.Print(err)
		return err
	}
	return nil
}

// Resume recreates the worktree and restarts the terminal session
func (i *Instance) Resume() error {
	if !i.started {
		return fmt.Errorf("cannot resume instance that has not been started")
	}
	if i.Status != Paused {
		return fmt.Errorf("can only resume paused instances")
	}

	// Check if branch is checked out
	if checked, err := i.gitWorktree.IsBranchCheckedOut(); err != nil {
		log.ErrorLog.Print(err)
		return fmt.Errorf("failed to check if branch is checked out: %w", err)
	} else if checked {
		return fmt.Errorf("cannot resume: branch is checked out, please switch to a different branch")
	}

	// Setup git worktree
	if err := i.gitWorktree.Setup(); err != nil {
		log.ErrorLog.Print(err)
		return fmt.Errorf("failed to setup git worktree: %w", err)
	}

	// Check if terminal session still exists from pause, otherwise create new one
	if i.termSession.DoesSessionExist() {
		// Session exists, just restore connection to it
		if err := i.termSession.Restore(); err != nil {
			log.ErrorLog.Print(err)
			// If restore fails, fall back to creating new session
			if err := i.termSession.Start(i.gitWorktree.GetWorktreePath()); err != nil {
				log.ErrorLog.Print(err)
				// Cleanup git worktree if session creation fails
				if cleanupErr := i.gitWorktree.Cleanup(); cleanupErr != nil {
					err = fmt.Errorf("%v (cleanup error: %v)", err, cleanupErr)
					log.ErrorLog.Print(err)
				}
				return fmt.Errorf("failed to start new session: %w", err)
			}
		}
	} else {
		// Create new terminal session
		if err := i.termSession.Start(i.gitWorktree.GetWorktreePath()); err != nil {
			log.ErrorLog.Print(err)
			// Cleanup git worktree if session creation fails
			if cleanupErr := i.gitWorktree.Cleanup(); cleanupErr != nil {
				err = fmt.Errorf("%v (cleanup error: %v)", err, cleanupErr)
				log.ErrorLog.Print(err)
			}
			return fmt.Errorf("failed to start new session: %w", err)
		}
	}

	i.SetStatus(Running)
	return nil
}

// UpdateDiffStats updates the git diff statistics for this instance
func (i *Instance) UpdateDiffStats() error {
	if !i.started {
		i.diffStats = nil
		return nil
	}

	if i.Status == Paused {
		// Keep the previous diff stats if the instance is paused
		return nil
	}

	stats := i.gitWorktree.Diff()
	if stats.Error != nil {
		if strings.Contains(stats.Error.Error(), "base commit SHA not set") {
			// Worktree is not fully set up yet, not an error
			i.diffStats = nil
			return nil
		}
		return fmt.Errorf("failed to get diff stats: %w", stats.Error)
	}

	i.diffStats = stats
	return nil
}

// ComputeDiff runs the expensive git diff I/O and returns the result without
// mutating instance state. Safe to call from a background goroutine.
func (i *Instance) ComputeDiff() *git.DiffStats {
	if !i.started || i.Status == Paused {
		return nil
	}
	return i.gitWorktree.Diff()
}

// ComputeDiffNumstat runs a lightweight git diff --numstat and returns only the
// added/removed line counts (Content is left empty). Safe to call from a
// background goroutine. Use this for instances whose full diff content is not
// currently needed so we avoid keeping large diffs in memory.
func (i *Instance) ComputeDiffNumstat() *git.DiffStats {
	if !i.started || i.Status == Paused {
		return nil
	}
	return i.gitWorktree.DiffNumstat()
}

// SetDiffStats sets the diff statistics on the instance. Should be called from
// the main event loop to avoid data races with View.
func (i *Instance) SetDiffStats(stats *git.DiffStats) {
	i.diffStats = stats
}

// GetDiffStats returns the current git diff statistics
func (i *Instance) GetDiffStats() *git.DiffStats {
	return i.diffStats
}

// SendPrompt sends a prompt to the terminal session
func (i *Instance) SendPrompt(prompt string) error {
	if !i.started {
		return fmt.Errorf("instance not started")
	}
	if i.termSession == nil {
		return fmt.Errorf("terminal session not initialized")
	}
	if err := i.termSession.SendKeys(prompt); err != nil {
		return fmt.Errorf("error sending keys to terminal session: %w", err)
	}

	// Brief pause to prevent carriage return from being interpreted as newline
	time.Sleep(100 * time.Millisecond)
	if err := i.termSession.TapEnter(); err != nil {
		return fmt.Errorf("error tapping enter: %w", err)
	}

	return nil
}

// PreviewFullHistory captures the entire terminal pane output including full scrollback history
func (i *Instance) PreviewFullHistory() (string, error) {
	if !i.started || i.Status == Paused {
		return "", nil
	}
	return i.termSession.CapturePaneContentWithOptions("-", "-")
}

// SetTerminalSession sets the terminal session for testing purposes
func (i *Instance) SetTerminalSession(session TerminalSession) {
	i.termSession = session
}

// SendKeys sends keys to the terminal session
func (i *Instance) SendKeys(keys string) error {
	if !i.started || i.Status == Paused {
		return fmt.Errorf("cannot send keys to instance that has not been started or is paused")
	}
	return i.termSession.SendKeys(keys)
}
