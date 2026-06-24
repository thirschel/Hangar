// Package copilotsdk wraps the GitHub Copilot SDK (github.com/github/copilot-sdk/go)
// as a Hangar agent session. It is the daemon-side backend for the opt-in,
// Copilot-only "rich agent view": instead of running the copilot CLI in a ConPTY
// and screen-scraping the terminal, it drives the CLI programmatically over
// JSON-RPC and exposes a structured event stream.
//
// The package is intentionally free of winhost/proto coupling so it can be unit
// tested in isolation; a thin adapter in session/winhost wraps a *Session to
// satisfy the host's managedSession interface (the Phase 0.5 "adapter" design).
package copilotsdk

import (
	"context"
	"fmt"
	"log"
	"os"
	"sync"
	"sync/atomic"
	"time"

	copilot "github.com/github/copilot-sdk/go"
	csrpc "github.com/github/copilot-sdk/go/rpc"
)

// Status is the high-level agent state used for sidebar/status reporting. It is
// derived from the SDK event stream rather than from scraped terminal output.
type Status int

const (
	StatusLoading Status = iota // starting up / not yet ready
	StatusReady                 // idle, awaiting user input
	StatusRunning               // mid-turn (the agent is producing output)
	StatusWaiting               // blocked on a permission/user-input decision
)

func (s Status) String() string {
	switch s {
	case StatusReady:
		return "ready"
	case StatusRunning:
		return "running"
	case StatusWaiting:
		return "waiting"
	default:
		return "loading"
	}
}

// PermissionDecider lets the host decide a permission request synchronously and
// WITHOUT blocking on IPC. Returning (approve=false, pend=true) declines to
// answer so a (re)attaching client can resolve it later — the AutoYes-OFF /
// detached model. (approve=false, pend=false) rejects.
type PermissionDecider func(req copilot.PermissionRequest) (approve, pend bool)

// Config configures a Session. Handlers are optional; safe non-blocking defaults
// are used so the daemon never hangs when no interactive client is attached.
type Config struct {
	WorkDir   string   // ClientOptions.WorkingDirectory (the git worktree)
	BaseDir   string   // ClientOptions.BaseDirectory (COPILOT_HOME); "" = default
	CLIPath   string   // explicit copilot CLI path; "" = PATH / COPILOT_CLI_PATH
	Env       []string // process env for the CLI; nil = inherit os.Environ()
	Model     string   // optional model id
	SessionID string   // stable id for resume (e.g. the Hangar workspace UUID)
	AutoYes   bool     // when true, auto-approve permission requests

	// MCPConfigPath is the copilot mcp-config.json to forward; "" = the default
	// ~/.copilot/mcp-config.json. Set DisableMCP to skip forwarding entirely.
	MCPConfigPath string
	DisableMCP    bool

	// OnEvent receives every SDK event (after internal status tracking). A nil
	// sink is tolerated (events are dropped) but yields no UI.
	OnEvent func(copilot.SessionEvent)
	// Decide overrides the permission policy. When nil, AutoYes governs: approve
	// when AutoYes is set, otherwise leave the request pending.
	Decide PermissionDecider

	Logger *log.Logger
}

// Session is a Hangar agent session backed by the Copilot SDK.
type Session struct {
	cfg    Config
	client *copilot.Client
	sess   *copilot.Session
	unsub  func()

	mu         sync.RWMutex
	status     Status
	started    bool
	lastOutput atomic.Int64 // unix-ms of the last output-changing event
}

// New builds a Session from cfg. Call Start (fresh) or Resume (existing id).
func New(cfg Config) *Session {
	if cfg.Logger == nil {
		cfg.Logger = log.New(os.Stderr, "[copilotsdk] ", log.LstdFlags)
	}
	return &Session{cfg: cfg, status: StatusLoading}
}

func (s *Session) clientOptions() *copilot.ClientOptions {
	opts := &copilot.ClientOptions{
		WorkingDirectory: s.cfg.WorkDir,
		BaseDirectory:    s.cfg.BaseDir,
	}
	if len(s.cfg.Env) > 0 {
		opts.Env = s.cfg.Env
	}
	if s.cfg.CLIPath != "" {
		opts.Connection = copilot.StdioConnection{Path: s.cfg.CLIPath}
	}
	return opts
}

func (s *Session) sessionConfig() *copilot.SessionConfig {
	sc := &copilot.SessionConfig{
		Streaming:            copilot.Bool(true),
		OnPermissionRequest:  s.onPermission,
		OnUserInputRequest:   s.onUserInput,   // always register, else ask_user blocks
		OnElicitationRequest: s.onElicitation, // always register, else elicitation blocks
	}
	if s.cfg.Model != "" {
		sc.Model = s.cfg.Model
	}
	if s.cfg.SessionID != "" {
		sc.SessionID = s.cfg.SessionID
	}
	if !s.cfg.DisableMCP {
		if servers, err := loadMCPServers(s.mcpConfigPath()); err != nil {
			s.cfg.Logger.Printf("mcp forward: %v (continuing without forwarded servers)", err)
		} else if len(servers) > 0 {
			sc.MCPServers = servers
		}
	}
	return sc
}

// Start launches the CLI runtime and creates a fresh session.
func (s *Session) Start(ctx context.Context) error { return s.start(ctx, false) }

// Resume launches the runtime and resumes Config.SessionID, replaying its
// transcript without re-running the model.
func (s *Session) Resume(ctx context.Context) error {
	if s.cfg.SessionID == "" {
		return fmt.Errorf("resume requires Config.SessionID")
	}
	return s.start(ctx, true)
}

func (s *Session) start(ctx context.Context, resume bool) error {
	s.mu.RLock()
	already := s.started
	s.mu.RUnlock()
	if already {
		return fmt.Errorf("session already started")
	}

	client := copilot.NewClient(s.clientOptions())
	if err := client.Start(ctx); err != nil {
		return fmt.Errorf("start copilot runtime: %w", err)
	}

	var (
		sess *copilot.Session
		err  error
	)
	if resume {
		sess, err = client.ResumeSession(ctx, s.cfg.SessionID, &copilot.ResumeSessionConfig{
			OnPermissionRequest:  s.onPermission,
			OnUserInputRequest:   s.onUserInput,
			OnElicitationRequest: s.onElicitation,
			Streaming:            copilot.Bool(true),
		})
	} else {
		sess, err = client.CreateSession(ctx, s.sessionConfig())
	}
	if err != nil {
		_ = client.Stop()
		return fmt.Errorf("create/resume session: %w", err)
	}
	unsub := sess.On(s.handleEvent)

	s.mu.Lock()
	s.client, s.sess, s.unsub, s.started, s.status = client, sess, unsub, true, StatusReady
	s.mu.Unlock()
	s.touch()
	return nil
}

// Send delivers a user message. It returns once the turn completes (the SDK
// resolves Send on idle); callers that want fire-and-forget should run it in a
// goroutine and observe the event stream.
func (s *Session) Send(ctx context.Context, prompt string) error {
	sess := s.session()
	if sess == nil {
		return fmt.Errorf("session not started")
	}
	s.setStatus(StatusRunning)
	_, err := sess.Send(ctx, copilot.MessageOptions{Prompt: prompt})
	return err
}

// Abort interrupts the current turn. Callers must let the session settle back to
// idle (StatusReady) before the next Send (mid-turn abort does not auto-idle).
func (s *Session) Abort(ctx context.Context) error {
	sess := s.session()
	if sess == nil {
		return fmt.Errorf("session not started")
	}
	return sess.Abort(ctx)
}

// Transcript returns the persisted event history, used to repaint a (re)attaching
// client without re-running the model. Survives compaction and daemon restarts.
func (s *Session) Transcript(ctx context.Context) ([]copilot.SessionEvent, error) {
	sess := s.session()
	if sess == nil {
		return nil, fmt.Errorf("session not started")
	}
	return sess.GetEvents(ctx)
}

// Close disconnects the session and stops the runtime.
func (s *Session) Close() error {
	s.mu.Lock()
	unsub, sess, client := s.unsub, s.sess, s.client
	s.sess, s.client, s.unsub, s.started = nil, nil, nil, false
	s.mu.Unlock()
	if unsub != nil {
		unsub()
	}
	if sess != nil {
		_ = sess.Disconnect()
	}
	if client != nil {
		return client.Stop()
	}
	return nil
}

// Status returns the current high-level state.
func (s *Session) Status() Status {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.status
}

// LastOutputUnixMs is the unix-ms time of the last output-changing event (0 if none).
func (s *Session) LastOutputUnixMs() int64 { return s.lastOutput.Load() }

func (s *Session) session() *copilot.Session {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.sess
}

func (s *Session) setStatus(st Status) {
	s.mu.Lock()
	s.status = st
	s.mu.Unlock()
}

func (s *Session) touch() { s.lastOutput.Store(time.Now().UnixMilli()) }

// handleEvent maps the SDK event stream onto Status, then forwards to the consumer.
func (s *Session) handleEvent(ev copilot.SessionEvent) {
	if ev.Data != nil {
		switch ev.Data.(type) {
		case *copilot.AssistantTurnStartData:
			s.setStatus(StatusRunning)
		case *copilot.PermissionRequestedData:
			s.setStatus(StatusWaiting)
		case *copilot.SessionIdleData:
			s.setStatus(StatusReady)
		}
		s.touch()
	}
	if s.cfg.OnEvent != nil {
		s.cfg.OnEvent(ev)
	}
}

// onPermission is the deadlock-free permission policy: it returns immediately,
// either auto-approving or declining-to-pending (NoResult) so a (re)attaching
// client can answer. It NEVER blocks on an IPC round-trip.
func (s *Session) onPermission(req copilot.PermissionRequest, _ copilot.PermissionInvocation) (csrpc.PermissionDecision, error) {
	approve, pend := s.decide(req)
	switch {
	case approve:
		return &csrpc.PermissionDecisionApproveOnce{}, nil
	case pend:
		return &csrpc.PermissionDecisionNoResult{}, nil // leave pending for another client
	default:
		return &csrpc.PermissionDecisionReject{}, nil
	}
}

func (s *Session) decide(req copilot.PermissionRequest) (approve, pend bool) {
	if s.cfg.Decide != nil {
		return s.cfg.Decide(req)
	}
	if s.cfg.AutoYes {
		return true, false
	}
	return false, true // default: leave pending for an interactive client
}

// onUserInput must answer synchronously (ask_user has no "pending" form). Until an
// interactive client is wired, decline so the daemon never hangs.
func (s *Session) onUserInput(_ copilot.UserInputRequest, _ copilot.UserInputInvocation) (copilot.UserInputResponse, error) {
	return copilot.UserInputResponse{}, fmt.Errorf("no interactive client available to answer ask_user")
}

func (s *Session) onElicitation(_ copilot.ElicitationContext) (copilot.ElicitationResult, error) {
	return copilot.ElicitationResult{}, fmt.Errorf("no interactive client available to answer elicitation")
}
