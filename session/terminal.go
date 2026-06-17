package session

// Program name suffixes for agents that have a startup trust/confirmation
// prompt claude-squad knows how to dismiss.
const (
	ProgramClaude = "claude"
	ProgramAider  = "aider"
	ProgramGemini = "gemini"
)

// TerminalSession is the interface for managing terminal sessions.
// On Unix this is backed by tmux, on Windows by Windows Terminal.
type TerminalSession interface {
	Start(workDir string) error
	Restore() error
	Close() error
	CapturePaneContent() (string, error)
	CapturePaneContentWithOptions(start, end string) (string, error)
	HasUpdated() (updated bool, hasPrompt bool)
	TapEnter() error
	TryAutoApprove(sessionID string) bool
	SetAutoYes(enabled bool) error
	SendKeys(keys string) error
	Attach() (chan struct{}, error)
	SetDetachedSize(width, height int) error
	DoesSessionExist() bool
	DetachSafely() error
	CheckAndHandleTrustPrompt() bool
}
