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
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log"
	"os"
	"strings"
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
	StatusExited                // the CLI child process exited/crashed (terminal)
)

func (s Status) String() string {
	switch s {
	case StatusReady:
		return "ready"
	case StatusRunning:
		return "running"
	case StatusWaiting:
		return "waiting"
	case StatusExited:
		return "exited"
	default:
		return "loading"
	}
}

// PermissionDecider lets the host decide a permission request synchronously and
// WITHOUT blocking on IPC. Returning (approve=false, pend=true) declines to
// answer so a (re)attaching client can resolve it later — the AutoYes-OFF /
// detached model. (approve=false, pend=false) rejects.
type PermissionDecider func(req copilot.PermissionRequest) (approve, pend bool)

// Prompt is a daemon-synthesized interactive prompt that must be surfaced to a
// client and correlated back with RespondUserInput.
type Prompt struct {
	Kind          string
	RequestID     string
	Question      string
	Choices       []string
	AllowFreeform bool
}

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
	// OnPrompt receives SDK handler prompts that are not emitted as SDK events
	// (ask_user / elicitation). The handler then blocks until RespondUserInput.
	OnPrompt func(Prompt)
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
	mcpServers []string
	autoYes    atomic.Bool  // runtime-toggleable auto-approval (host SetAutoYes)
	lastOutput atomic.Int64 // unix-ms of the last output-changing event

	promptMu sync.Mutex
	prompts  map[string]chan userInputReply
	closing  bool
}

type userInputReply struct {
	answer   string
	freeform bool
	ok       bool
}

// New builds a Session from cfg. Call Start (fresh) or Resume (existing id).
func New(cfg Config) *Session {
	if cfg.Logger == nil {
		cfg.Logger = log.New(os.Stderr, "[copilotsdk] ", log.LstdFlags)
	}
	s := &Session{cfg: cfg, status: StatusLoading, prompts: make(map[string]chan userInputReply)}
	s.autoYes.Store(cfg.AutoYes)
	return s
}

// SetAutoYes toggles auto-approval at runtime (e.g. from the host's SetAutoYes RPC).
func (s *Session) SetAutoYes(v bool) { s.autoYes.Store(v) }

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
		OnEvent:              s.handleEvent,
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
		if servers := s.forwardedMCPServers(); len(servers) > 0 {
			sc.MCPServers = servers
		}
	} else {
		s.setMCPServerNames(nil)
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
		resumeConfig := &copilot.ResumeSessionConfig{
			OnEvent:              s.handleEvent,
			OnPermissionRequest:  s.onPermission,
			OnUserInputRequest:   s.onUserInput,
			OnElicitationRequest: s.onElicitation,
			Streaming:            copilot.Bool(true),
		}
		if !s.cfg.DisableMCP {
			if servers := s.forwardedMCPServers(); len(servers) > 0 {
				resumeConfig.MCPServers = servers
			}
		} else {
			s.setMCPServerNames(nil)
		}
		sess, err = client.ResumeSession(ctx, s.cfg.SessionID, resumeConfig)
	} else {
		sess, err = client.CreateSession(ctx, s.sessionConfig())
	}
	if err != nil {
		_ = client.Stop()
		return fmt.Errorf("create/resume session: %w", err)
	}
	s.mu.Lock()
	s.client, s.sess, s.unsub, s.started, s.status = client, sess, nil, true, StatusReady
	s.mu.Unlock()
	s.promptMu.Lock()
	s.closing = false
	s.promptMu.Unlock()
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
	return s.noteErr(err)
}

// Abort interrupts the current turn. Callers must let the session settle back to
// idle (StatusReady) before the next Send (mid-turn abort does not auto-idle).
func (s *Session) Abort(ctx context.Context) error {
	sess := s.session()
	if sess == nil {
		return fmt.Errorf("session not started")
	}
	err := sess.Abort(ctx)
	// Unblock any ask_user/elicitation handler parked on the aborted turn so the
	// SDK handler goroutine returns promptly (declined) instead of waiting for an
	// answer or session close — the turn it belonged to is gone.
	s.abortPrompts()
	return s.noteErr(err)
}

// RespondPermission resolves a pending permission request out-of-band by the
// SDK requestID emitted on permission.requested events.
func (s *Session) RespondPermission(ctx context.Context, requestID string, approve bool) error {
	sess := s.session()
	if sess == nil {
		return fmt.Errorf("session not started")
	}
	if sess.RPC == nil || sess.RPC.Permissions == nil {
		return fmt.Errorf("session permissions RPC is unavailable")
	}
	var decision csrpc.PermissionDecision
	if approve {
		decision = &csrpc.PermissionDecisionApproveOnce{}
	} else {
		decision = &csrpc.PermissionDecisionReject{}
	}
	_, err := sess.RPC.Permissions.HandlePendingPermissionRequest(ctx, &csrpc.PermissionDecisionRequest{
		RequestID: requestID,
		Result:    decision,
	})
	return s.noteErr(err)
}

// Transcript returns the persisted event history, used to repaint a (re)attaching
// client without re-running the model. Survives compaction and daemon restarts.
func (s *Session) Transcript(ctx context.Context) ([]copilot.SessionEvent, error) {
	sess := s.session()
	if sess == nil {
		return nil, fmt.Errorf("session not started")
	}
	evs, err := sess.GetEvents(ctx)
	return evs, s.noteErr(err)
}

// Close disconnects the session and stops the runtime.
func (s *Session) Close() error {
	s.mu.Lock()
	unsub, sess, client := s.unsub, s.sess, s.client
	s.sess, s.client, s.unsub, s.started = nil, nil, nil, false
	s.mu.Unlock()
	s.closePrompts()
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

// MCPServerNames returns the names of MCP servers forwarded into the SDK session.
// It intentionally exposes names only, never URLs, headers, tokens, or commands.
func (s *Session) MCPServerNames() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if len(s.mcpServers) == 0 {
		return nil
	}
	out := make([]string, len(s.mcpServers))
	copy(out, s.mcpServers)
	return out
}

func (s *Session) session() *copilot.Session {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.sess
}

func (s *Session) forwardedMCPServers() map[string]copilot.MCPServerConfig {
	servers, err := loadMCPServers(s.mcpConfigPath())
	if err != nil {
		s.cfg.Logger.Printf("mcp forward: %v (continuing without forwarded servers)", err)
		s.setMCPServerNames(nil)
		return nil
	}
	s.setMCPServerNames(sortedMCPServerNames(servers))
	return servers
}

func (s *Session) setMCPServerNames(names []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(names) == 0 {
		s.mcpServers = nil
		return
	}
	s.mcpServers = append([]string(nil), names...)
}

func (s *Session) setStatus(st Status) {
	s.mu.Lock()
	if s.status != StatusExited { // StatusExited is terminal/sticky
		s.status = st
	}
	s.mu.Unlock()
}

// Exited reports whether the CLI child process has exited/crashed. A crashed
// child emits NO SDK event (verified by spike), so this is detected reactively
// when an operation returns a "process exited" error. The host marks the session
// not-alive so the next OpenRichStream revives (resumes) it.
func (s *Session) Exited() bool { return s.Status() == StatusExited }

// noteErr flags the session as exited when err indicates the CLI child process
// died, so Exited()/alive() reflect the crash. Returns err unchanged.
func (s *Session) noteErr(err error) error {
	if isProcessExited(err) {
		s.setStatus(StatusExited)
	}
	return err
}

// isProcessExited matches the SDK's child-process-death errors ("CLI process
// exited", "CLI process exited unexpectedly", "process exited unexpectedly").
// "client stopped" (our own Close) is deliberately excluded.
func isProcessExited(err error) bool {
	return err != nil && strings.Contains(err.Error(), "process exited")
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
	if s.autoYes.Load() {
		return true, false
	}
	return false, true // default: leave pending for an interactive client
}

// onUserInput must answer synchronously (ask_user has no requestID-keyed
// out-of-band resolve). The SDK invokes it on its own goroutine, so it is safe to
// block until the host answers through RespondUserInput.
func (s *Session) onUserInput(req copilot.UserInputRequest, _ copilot.UserInputInvocation) (copilot.UserInputResponse, error) {
	allowFreeform := false
	if req.AllowFreeform != nil {
		allowFreeform = *req.AllowFreeform
	}
	reply, err := s.promptAndWait(Prompt{
		Kind:          "user_input",
		Question:      req.Question,
		Choices:       append([]string(nil), req.Choices...),
		AllowFreeform: allowFreeform,
	})
	if err != nil {
		return copilot.UserInputResponse{}, err
	}
	return copilot.UserInputResponse{Answer: reply.answer, WasFreeform: reply.freeform}, nil
}

func (s *Session) onElicitation(req copilot.ElicitationContext) (copilot.ElicitationResult, error) {
	reply, err := s.promptAndWait(Prompt{
		Kind:          "elicitation",
		Question:      req.Message,
		AllowFreeform: true,
	})
	if err != nil {
		return copilot.ElicitationResult{Action: copilot.ElicitationActionDecline}, err
	}
	field := "answer"
	if req.RequestedSchema != nil {
		for name := range req.RequestedSchema.Properties {
			field = name
			break
		}
	}
	return copilot.ElicitationResult{
		Action:  copilot.ElicitationActionAccept,
		Content: map[string]copilot.ElicitationFieldValue{field: reply.answer},
	}, nil
}

func (s *Session) promptAndWait(prompt Prompt) (userInputReply, error) {
	if s.cfg.OnPrompt == nil {
		return userInputReply{}, fmt.Errorf("no interactive client available to answer %s", prompt.Kind)
	}
	id, err := newPromptRequestID()
	if err != nil {
		return userInputReply{}, err
	}
	prompt.RequestID = id
	ch := make(chan userInputReply, 1)

	s.promptMu.Lock()
	if s.closing {
		s.promptMu.Unlock()
		return userInputReply{}, fmt.Errorf("session is closing")
	}
	s.prompts[id] = ch
	s.promptMu.Unlock()

	s.setStatus(StatusWaiting)
	s.touch()
	s.cfg.OnPrompt(prompt)
	reply := <-ch
	if !reply.ok {
		return userInputReply{}, fmt.Errorf("session closed before %s was answered", prompt.Kind)
	}
	return reply, nil
}

// RespondUserInput answers a pending ask_user or elicitation prompt generated by
// this Session.
func (s *Session) RespondUserInput(requestID, answer string, freeform bool) error {
	s.promptMu.Lock()
	ch, ok := s.prompts[requestID]
	if ok {
		delete(s.prompts, requestID)
		ch <- userInputReply{answer: answer, freeform: freeform, ok: true}
	}
	s.promptMu.Unlock()
	if !ok {
		return fmt.Errorf("no pending user input request: %s", requestID)
	}
	return nil
}

func (s *Session) closePrompts() {
	s.promptMu.Lock()
	s.closing = true
	for id, ch := range s.prompts {
		delete(s.prompts, id)
		ch <- userInputReply{ok: false}
	}
	s.promptMu.Unlock()
}

// abortPrompts unblocks pending user-input/elicitation prompts (declining them)
// WITHOUT closing the session, so a mid-turn Abort does not leave handler
// goroutines parked until the session ends. The session stays reusable.
func (s *Session) abortPrompts() {
	s.promptMu.Lock()
	for id, ch := range s.prompts {
		delete(s.prompts, id)
		ch <- userInputReply{ok: false}
	}
	s.promptMu.Unlock()
}

func newPromptRequestID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("generate prompt request id: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}
