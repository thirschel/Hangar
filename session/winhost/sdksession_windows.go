//go:build windows

package winhost

import (
	"context"
	"fmt"
	stdlog "log"
	"sync"
	"time"

	copilot "github.com/github/copilot-sdk/go"
	"hangar/session/copilotsdk"
	"hangar/session/winhost/proto"
)

// sdkSession adapts a copilotsdk.Session to the host's managedSession interface
// (the Phase 0.5 "adapter" design). It lets a Copilot SDK-backed "rich" session
// live in the same host registry as ConPTY sessions WITHOUT widening the registry
// or breaking toInfo()/the TUI: terminal-shaped methods are mapped from SDK state
// or no-op'd. The structured event stream (Phase 2/3) is delivered out of band,
// not through the byte-oriented subscribe() path.
type sdkSession struct {
	name    string
	program string
	sess    *copilotsdk.Session
	logger  *stdlog.Logger

	// effort/contextTier (v18) are the persisted reasoning-effort and context-tier
	// selections threaded in at (re)create time. They are echoed back on the model
	// frame (emitModelFrame) so a restarted desktop restores the model selector;
	// the active model itself is read from the copilotsdk session (CurrentModel).
	effort      string
	contextTier string

	// sendFn delivers a user message (text + absolute attachment paths) to the
	// underlying SDK session. nil means call sess.Send directly; tests override it
	// to observe what richSend threads through, without a live Copilot CLI.
	sendFn func(ctx context.Context, text string, attachments []string) error

	ctx    context.Context
	cancel context.CancelFunc

	mu          sync.Mutex
	subs        map[*subscriber]struct{}
	richLog     []proto.EventFrame
	richSeq     uint64
	richSubs    map[*richSub]struct{}
	bufferMCP   bool
	bufferedMCP []copilot.SessionEvent
	lastMCPSig  string // last emitted resume-poll MCP status fingerprint (poll goroutine only)
	lastSeen    int64  // lastOutputUnixMs observed at the previous hasUpdated() call
	exitCode    int
	closed      bool
	exitedNoted bool // true once the agent-process-exit error frame has been emitted
}

// sdkSessionParams bundles the inputs needed to (re)create a rich (Copilot SDK)
// session. It is shared by newSDKSession and host.startSDKSession so the rich
// create / revive / regenerate / resume paths thread the same fields — including
// the per-repo Hangar MCP catalog overlay (extraMCP) — through one options struct
// instead of a long positional argument list. resume selects start vs
// startResumed and is consumed by startSDKSession, not newSDKSession.
type sdkSessionParams struct {
	name        string
	program     string
	workDir     string // the git worktree (ClientOptions.WorkingDirectory)
	baseDir     string // COPILOT_HOME override; "" = default
	autoYes     bool
	sessionID   string
	model       string
	effort      string
	contextTier string
	resume      bool
	// extraMCP are the Hangar MCP catalog servers enabled for this workspace's repo
	// (session/winhost.enabledMCPFor). They are forwarded on top of the copilot
	// CLI's mcp-config.json in copilotsdk.forwardedMCPServers, where the catalog
	// wins on a name collision. nil = none.
	extraMCP map[string]copilot.MCPServerConfig
}

// newSDKSession builds a rich SDK-backed session from p. p.workDir is the git
// worktree; p.baseDir overrides COPILOT_HOME (empty = default). p.model/effort/
// contextTier (v18) seed the Copilot SDK model selection so a (re)created session
// restores the user's choice (empty = the SDK default, a fresh chat). p.extraMCP
// are the per-repo Hangar MCP catalog servers, forwarded alongside the CLI's
// mcp-config.json. onEvent (optional) receives the structured event stream for the
// rich-view pipe. p.resume is not consumed here (startSDKSession uses it).
func newSDKSession(p sdkSessionParams, onEvent func(copilot.SessionEvent), logger *stdlog.Logger) *sdkSession {
	ctx, cancel := context.WithCancel(context.Background())
	s := &sdkSession{
		name:        p.name,
		program:     p.program,
		effort:      p.effort,
		contextTier: p.contextTier,
		logger:      logger,
		ctx:         ctx,
		cancel:      cancel,
		subs:        make(map[*subscriber]struct{}),
		richSubs:    make(map[*richSub]struct{}),
	}
	s.sess = copilotsdk.New(copilotsdk.Config{
		WorkDir:         p.workDir,
		BaseDir:         p.baseDir,
		SessionID:       p.sessionID,
		Model:           p.model,
		ReasoningEffort: p.effort,
		ContextTier:     p.contextTier,
		AutoYes:         p.autoYes,
		ExtraMCPServers: p.extraMCP,
		OnEvent: func(ev copilot.SessionEvent) {
			s.onSDKEvent(ev)
			if onEvent != nil {
				onEvent(ev)
			}
		},
		OnPrompt: s.onSDKPrompt,
		Logger:   logger,
	})
	return s
}

func (s *sdkSession) start() error {
	s.beginMCPStartupBuffer()
	if err := s.sess.Start(s.ctx); err != nil {
		s.cancelMCPStartupBuffer()
		return err
	}
	s.emitConfiguredMCPServersPending()
	s.flushMCPStartupBuffer()
	return nil
}

func (s *sdkSession) startResumed() error {
	s.beginMCPStartupBuffer()
	if err := s.sess.Resume(s.ctx); err != nil {
		s.cancelMCPStartupBuffer()
		s.logf("SDK resume failed for session %q: %v; starting fresh", s.name, err)
		s.beginMCPStartupBuffer()
		if startErr := s.sess.Start(s.ctx); startErr != nil {
			s.cancelMCPStartupBuffer()
			return fmt.Errorf("resume sdk session: %v; fresh start: %w", err, startErr)
		}
		s.emitConfiguredMCPServersPending()
		s.flushMCPStartupBuffer()
		return nil
	}
	s.emitConfiguredMCPServersPending()
	s.flushMCPStartupBuffer()
	evs, err := s.sess.Transcript(s.ctx)
	if err != nil {
		s.logf("SDK transcript replay failed for session %q: %v", s.name, err)
		return nil
	}
	for _, ev := range evs {
		// MCP status and skills are re-pulled live on resume (MCP via RPC.MCP.List
		// polling below, skills via RPC.Skills.Discover), so skip replaying their
		// historical events.
		if isMCPStatusEvent(ev) || isSkillsEvent(ev) {
			continue
		}
		s.translateAndEmit(ev)
	}
	// Proactively refresh live session state (MCP status, AIC, context window) that the
	// transcript replay does not carry, so those panes populate without the first turn.
	s.refreshResumedSessionState()
	// Custom instructions, agents, and skills arrive via RPC pulls (not the event
	// stream), so emit one-time snapshots on stream start so their pages populate
	// (v23/v24). Skills are additionally re-emitted on each live skills-loaded event.
	s.emitInstructions(s.ctx)
	s.emitAgents(s.ctx)
	s.emitSkills(s.ctx)
	// If the replayed transcript was interrupted before its terminal frame (e.g. the
	// daemon was killed mid-turn), settle the dangling turn so the resumed session
	// presents as idle instead of stuck "running".
	s.emitIdleIfDangling()
	return nil
}

func (s *sdkSession) beginMCPStartupBuffer() {
	s.mu.Lock()
	s.bufferMCP = true
	s.bufferedMCP = nil
	s.mu.Unlock()
}

func (s *sdkSession) bufferMCPStartupEvent(ev copilot.SessionEvent) bool {
	if !isMCPStatusEvent(ev) {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.bufferMCP {
		return false
	}
	s.bufferedMCP = append(s.bufferedMCP, ev)
	return true
}

func (s *sdkSession) flushMCPStartupBuffer() {
	for {
		s.mu.Lock()
		if len(s.bufferedMCP) == 0 {
			s.bufferMCP = false
			s.mu.Unlock()
			return
		}
		buffered := append([]copilot.SessionEvent(nil), s.bufferedMCP...)
		s.bufferedMCP = nil
		s.mu.Unlock()

		for _, ev := range buffered {
			s.translateAndEmit(ev)
		}
	}
}

func (s *sdkSession) cancelMCPStartupBuffer() {
	s.mu.Lock()
	s.bufferedMCP = nil
	s.bufferMCP = false
	s.mu.Unlock()
}

func (s *sdkSession) emitConfiguredMCPServersPending() {
	s.emitMCPServerPendingFrames(s.sess.MCPServerNames())
	// Mirror the pill-bar pending names with a single mcp.detail snapshot so the
	// rich MCP page populates immediately; real status/transport/source arrive via
	// the live (buffered then flushed) MCP events.
	s.emitMCPDetail()
}

func (s *sdkSession) emitMCPServerPendingFrames(names []string) {
	for _, name := range names {
		if name == "" {
			continue
		}
		s.emitFrame(proto.EventFrame{
			Kind:      proto.EventKindMCPStatus,
			MCPServer: name,
			Status:    "pending",
		})
	}
}

func (s *sdkSession) logf(format string, args ...any) {
	if s.logger != nil {
		s.logger.Printf(format, args...)
	}
}

// capture renders nothing for the byte path; rich sessions render from the
// structured event stream (Phase 3). Returning "" keeps any terminal-shaped
// consumer safe (no panic).
func (s *sdkSession) capture(full, withANSI bool) string { return "" }

func (s *sdkSession) captureHistory(includeScreen bool, cols, rows int) (string, bool, int) {
	return "", false, 0
}

// sendKeys is a no-op for rich sessions; input arrives via the structured control
// channel (SendMessage), not as raw keystrokes.
func (s *sdkSession) sendKeys(b []byte) error { return nil }

func (s *sdkSession) resize(cols, rows int) error { return nil }

func (s *sdkSession) hasUpdated() (updated, hasPrompt bool) {
	cur := s.sess.LastOutputUnixMs()
	s.mu.Lock()
	updated = cur != s.lastSeen
	s.lastSeen = cur
	s.mu.Unlock()
	_, waiting := s.agentStatus()
	return updated, waiting
}

func (s *sdkSession) agentStatus() (busy, waiting bool) {
	switch s.sess.Status() {
	case copilotsdk.StatusRunning:
		return true, false
	case copilotsdk.StatusWaiting:
		return false, true
	default:
		return false, false
	}
}

func (s *sdkSession) bracketedPasteEnabled() bool { return false }

func (s *sdkSession) lastOutputUnixMs() int64 { return s.sess.LastOutputUnixMs() }

func (s *sdkSession) setAutoYes(enabled bool) { s.sess.SetAutoYes(enabled) }

func (s *sdkSession) onSDKPrompt(prompt copilotsdk.Prompt) {
	if prompt.Kind != "user_input" && prompt.Kind != "elicitation" {
		return
	}
	s.emitFrame(proto.EventFrame{
		Kind:      proto.EventKindUserInputRequest,
		RequestID: prompt.RequestID,
		Question:  prompt.Question,
		Choices:   append([]string(nil), prompt.Choices...),
	})
}

// Trust prompts are a terminal concept; the SDK gates tools structurally.
func (s *sdkSession) armTrustApproval(reason string, expiresAt time.Time) {}
func (s *sdkSession) clearTrustApproval()                                 {}

func (s *sdkSession) info() proto.SessionInfo {
	return proto.SessionInfo{
		Name:     s.name,
		Alive:    s.alive(),
		ExitCode: s.exitCode,
		Program:  s.program,
	}
}

func (s *sdkSession) alive() bool {
	if s.sess.Exited() { // CLI child crashed: not alive, so revive recreates it
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return !s.closed
}

func (s *sdkSession) close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	subs := s.subs
	s.subs = make(map[*subscriber]struct{})
	richSubs := s.richSubs
	s.richSubs = make(map[*richSub]struct{})
	s.mu.Unlock()
	s.cancel()
	for sub := range subs {
		close(sub.ch)
	}
	for sub := range richSubs {
		close(sub.ch)
	}
	return s.sess.Close()
}

// subscribe returns a nil snapshot and a subscriber that receives no bytes: rich
// sessions stream structured events out of band, not through this path.
func (s *sdkSession) subscribe(cols, rows int) ([]byte, *subscriber) {
	sub := &subscriber{ch: make(chan []byte, 1)}
	s.mu.Lock()
	if !s.closed {
		s.subs[sub] = struct{}{}
	}
	s.mu.Unlock()
	return nil, sub
}

func (s *sdkSession) unsubscribe(sub *subscriber) {
	s.mu.Lock()
	if _, ok := s.subs[sub]; ok {
		delete(s.subs, sub)
		close(sub.ch)
	}
	s.mu.Unlock()
}

// compile-time assertion that the adapter satisfies the host's session interface.
var _ managedSession = (*sdkSession)(nil)
