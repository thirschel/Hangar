//go:build windows

package winhost

import (
	"context"
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

	ctx    context.Context
	cancel context.CancelFunc

	mu       sync.Mutex
	subs     map[*subscriber]struct{}
	richLog  []proto.EventFrame
	richSeq  uint64
	richSubs map[*richSub]struct{}
	lastSeen int64 // lastOutputUnixMs observed at the previous hasUpdated() call
	exitCode int
	closed   bool
}

// newSDKSession builds a rich SDK-backed session. workDir is the git worktree;
// baseDir overrides COPILOT_HOME (empty = default). onEvent (optional) receives
// the structured event stream for the eventual rich-view pipe.
func newSDKSession(name, program, workDir, baseDir string, autoYes bool, sessionID string, onEvent func(copilot.SessionEvent), logger *stdlog.Logger) *sdkSession {
	ctx, cancel := context.WithCancel(context.Background())
	s := &sdkSession{
		name:     name,
		program:  program,
		ctx:      ctx,
		cancel:   cancel,
		subs:     make(map[*subscriber]struct{}),
		richSubs: make(map[*richSub]struct{}),
	}
	s.sess = copilotsdk.New(copilotsdk.Config{
		WorkDir:   workDir,
		BaseDir:   baseDir,
		SessionID: sessionID,
		AutoYes:   autoYes,
		OnEvent: func(ev copilot.SessionEvent) {
			s.onSDKEvent(ev)
			if onEvent != nil {
				onEvent(ev)
			}
		},
		Logger: logger,
	})
	return s
}

func (s *sdkSession) start() error { return s.sess.Start(s.ctx) }

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
