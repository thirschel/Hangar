//go:build windows

package winhost

import (
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"sync"
	"time"

	"claude-squad/session/winhost/proto"

	"github.com/Microsoft/go-winio"
)

// idleTimeout: the host self-exits after this long with zero sessions and zero
// connected clients (matches tmux-server-like behaviour without lingering
// forever). Overridable in tests via newHost.
const defaultIdleTimeout = 5 * time.Minute

// bodyReadTimeout bounds how long the host waits for a frame body once its
// header has arrived (anti-stuck-client). The header read itself is unbounded so
// idle persistent control connections stay open.
const bodyReadTimeout = 15 * time.Second

// echoSession is a placeholder session used in P1 to exercise the full protocol
// and lifecycle without a real ConPTY. P2 replaces it with a ConPTY + VT
// emulator backed session implementing the same surface.
type echoSession struct {
	name    string
	program string
	workDir string

	mu       sync.Mutex
	autoYes  bool
	buf      []byte
	changed  bool
	alive    bool
	exitCode int
}

func (s *echoSession) sendKeys(b []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.buf = append(s.buf, b...)
	s.changed = true
}

func (s *echoSession) capture() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return string(s.buf)
}

func (s *echoSession) hasUpdated() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	u := s.changed
	s.changed = false
	return u
}

type host struct {
	mu          sync.RWMutex
	sessions    map[string]*echoSession
	activeConns int
	lastActive  time.Time
	idleTimeout time.Duration

	ln           net.Listener
	logger       *log.Logger
	shutdownCh   chan struct{}
	shutdownOnce sync.Once
}

func newHost(logw io.Writer, idle time.Duration) *host {
	return &host{
		sessions:    make(map[string]*echoSession),
		lastActive:  time.Now(),
		idleTimeout: idle,
		logger:      log.New(logw, "[host] ", log.LstdFlags|log.Lmicroseconds),
		shutdownCh:  make(chan struct{}),
	}
}

func (h *host) touch() {
	h.mu.Lock()
	h.lastActive = time.Now()
	h.mu.Unlock()
}

func (h *host) serve(ln net.Listener) {
	h.ln = ln
	for {
		conn, err := ln.Accept()
		if err != nil {
			select {
			case <-h.shutdownCh:
				return
			default:
				h.logger.Printf("accept error: %v", err)
				return
			}
		}
		h.mu.Lock()
		h.activeConns++
		h.mu.Unlock()
		go h.handleConn(conn)
	}
}

func (h *host) handleConn(conn net.Conn) {
	defer func() {
		_ = conn.Close()
		h.mu.Lock()
		h.activeConns--
		h.lastActive = time.Now()
		h.mu.Unlock()
	}()
	for {
		// Unbounded wait for the next request header (persistent connection)...
		n, err := proto.ReadHeader(conn)
		if err != nil {
			return
		}
		// ...but bound the body read so a half-sent frame can't park us.
		_ = conn.SetReadDeadline(time.Now().Add(bodyReadTimeout))
		body, err := proto.ReadBody(conn, n)
		_ = conn.SetReadDeadline(time.Time{})
		if err != nil {
			return
		}
		req, err := proto.DecodeRequest(body)
		if err != nil {
			return
		}
		resp := h.dispatch(req)
		_ = conn.SetWriteDeadline(time.Now().Add(30 * time.Second))
		if err := proto.WriteFrame(conn, resp); err != nil {
			return
		}
		_ = conn.SetWriteDeadline(time.Time{})
		if req.Method == proto.MethodShutdown {
			h.logger.Printf("shutdown requested by client")
			h.triggerShutdown()
			return
		}
	}
}

func (h *host) dispatch(req *proto.Request) *proto.Response {
	h.touch()
	switch req.Method {
	case proto.MethodHello:
		return &proto.Response{ID: req.ID, OK: true, HostVersion: proto.Version}
	case proto.MethodCreateSession:
		return h.createSession(req)
	case proto.MethodHasSession:
		s, ok := h.getSession(req.Session)
		r := &proto.Response{ID: req.ID, OK: true, Exists: ok}
		if ok {
			s.mu.Lock()
			r.Alive = s.alive
			s.mu.Unlock()
		}
		return r
	case proto.MethodListSessions:
		return &proto.Response{ID: req.ID, OK: true, Sessions: h.listSessions()}
	case proto.MethodCapturePane:
		s, ok := h.getSession(req.Session)
		if !ok {
			return proto.Errorf(req.ID, "no such session: %s", req.Session)
		}
		return &proto.Response{ID: req.ID, OK: true, Content: s.capture()}
	case proto.MethodSendKeys:
		s, ok := h.getSession(req.Session)
		if !ok {
			return proto.Errorf(req.ID, "no such session: %s", req.Session)
		}
		s.sendKeys(req.Data)
		return &proto.Response{ID: req.ID, OK: true}
	case proto.MethodResize:
		if _, ok := h.getSession(req.Session); !ok {
			return proto.Errorf(req.ID, "no such session: %s", req.Session)
		}
		return &proto.Response{ID: req.ID, OK: true} // echo: no-op
	case proto.MethodHasUpdated:
		s, ok := h.getSession(req.Session)
		if !ok {
			return proto.Errorf(req.ID, "no such session: %s", req.Session)
		}
		return &proto.Response{ID: req.ID, OK: true, Updated: s.hasUpdated(), HasPrompt: false}
	case proto.MethodSetAutoYes:
		s, ok := h.getSession(req.Session)
		if !ok {
			return proto.Errorf(req.ID, "no such session: %s", req.Session)
		}
		s.mu.Lock()
		s.autoYes = req.Enabled
		s.mu.Unlock()
		return &proto.Response{ID: req.ID, OK: true}
	case proto.MethodKillSession:
		h.killSession(req.Session)
		return &proto.Response{ID: req.ID, OK: true}
	case proto.MethodShutdown:
		return &proto.Response{ID: req.ID, OK: true}
	default:
		return proto.Errorf(req.ID, "unknown method %q", req.Method)
	}
}

func (h *host) createSession(req *proto.Request) *proto.Response {
	if req.Session == "" {
		return proto.Errorf(req.ID, "session name required")
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, exists := h.sessions[req.Session]; exists {
		return proto.Errorf(req.ID, "session already exists: %s", req.Session)
	}
	s := &echoSession{
		name:    req.Session,
		program: req.Program,
		workDir: req.WorkDir,
		autoYes: req.AutoYes,
		alive:   true,
		buf:     []byte(fmt.Sprintf("[echo session %q running %q]\n", req.Session, req.Program)),
	}
	h.sessions[req.Session] = s
	h.lastActive = time.Now()
	h.logger.Printf("created session %q (program=%q)", req.Session, req.Program)
	return &proto.Response{ID: req.ID, OK: true}
}

func (h *host) getSession(name string) (*echoSession, bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	s, ok := h.sessions[name]
	return s, ok
}

func (h *host) listSessions() []proto.SessionInfo {
	h.mu.RLock()
	defer h.mu.RUnlock()
	out := make([]proto.SessionInfo, 0, len(h.sessions))
	for _, s := range h.sessions {
		s.mu.Lock()
		out = append(out, proto.SessionInfo{Name: s.name, Alive: s.alive, ExitCode: s.exitCode, Program: s.program})
		s.mu.Unlock()
	}
	return out
}

func (h *host) killSession(name string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if s, ok := h.sessions[name]; ok {
		s.mu.Lock()
		s.alive = false
		s.mu.Unlock()
		delete(h.sessions, name)
		h.logger.Printf("killed session %q", name)
	}
}

func (h *host) idleLoop() {
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-h.shutdownCh:
			return
		case <-t.C:
			h.mu.RLock()
			idle := h.activeConns == 0 && len(h.sessions) == 0 &&
				time.Since(h.lastActive) > h.idleTimeout
			h.mu.RUnlock()
			if idle {
				h.logger.Printf("idle for %s with no sessions/clients; shutting down", h.idleTimeout)
				h.triggerShutdown()
				return
			}
		}
	}
}

func (h *host) triggerShutdown() {
	h.shutdownOnce.Do(func() {
		close(h.shutdownCh)
		if h.ln != nil {
			_ = h.ln.Close()
		}
	})
}

// RunHost is the entry point for `cs session-host`. It runs the detached host
// daemon, blocking until shutdown. A second invocation exits immediately because
// it cannot take the singleton lock.
func RunHost() error {
	lockPath, err := hostLockPath()
	if err != nil {
		return err
	}
	lock, err := acquireLock(lockPath)
	if err != nil {
		// Another host is already running. Nothing to do.
		return nil
	}
	defer releaseLock(lock)

	var logw io.Writer = io.Discard
	if lp, err := hostLogPath(); err == nil {
		if f, err := os.OpenFile(lp, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600); err == nil {
			logw = f
			defer f.Close()
		}
	}

	pipe, err := controlPipeName()
	if err != nil {
		return err
	}
	sddl, err := currentUserSDDL()
	if err != nil {
		return err
	}
	ln, err := winio.ListenPipe(pipe, &winio.PipeConfig{SecurityDescriptor: sddl})
	if err != nil {
		return fmt.Errorf("listen pipe %s: %w", pipe, err)
	}

	h := newHost(logw, defaultIdleTimeout)
	if err := writeHostInfo(pipe); err != nil {
		h.logger.Printf("warning: could not write host.json: %v", err)
	}
	defer removeHostInfo()

	go h.idleLoop()
	h.logger.Printf("session-host started pid=%d pipe=%s version=%d", os.Getpid(), pipe, proto.Version)
	h.serve(ln) // blocks until shutdown
	h.logger.Printf("session-host stopped")
	return nil
}
