//go:build windows

package winhost

import (
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"time"

	cslog "hangar/log"
	"hangar/session/winhost/proto"

	"github.com/Microsoft/go-winio"
)

// defaultIdleTimeout: the host self-exits after this long with zero sessions and
// zero connected clients (tmux-server-like, without lingering forever).
const defaultIdleTimeout = 5 * time.Minute

// bodyReadTimeout bounds how long the host waits for a frame body once its
// header has arrived (anti-stuck-client). The header read itself is unbounded so
// idle persistent control connections stay open.
const bodyReadTimeout = 15 * time.Second

// managedSession is the host's view of a session. The real implementation is
// conptySession (ConPTY + VT emulator); tests inject a fake via host.newSession.
type managedSession interface {
	start() error
	capture(full, withANSI bool) string
	sendKeys(b []byte) error
	resize(cols, rows int) error
	hasUpdated() (updated, hasPrompt bool)
	agentStatus() (busy, waiting bool)
	setAutoYes(enabled bool)
	setForceApprove(enabled bool)
	info() proto.SessionInfo
	alive() bool
	close() error
	subscribe(cols, rows int) (snapshot []byte, sub *subscriber)
	unsubscribe(sub *subscriber)
}

type host struct {
	mu          sync.RWMutex
	sessions    map[string]managedSession
	newSession  func(name, program, workDir string, cols, rows int, autoYes bool) managedSession
	activeConns int
	lastActive  time.Time
	idleTimeout time.Duration

	ln           net.Listener
	logger       *log.Logger
	shutdownCh   chan struct{}
	shutdownOnce sync.Once
	attachSeq    atomic.Uint64

	workspaces *workspaceManager
	runs       *runManager
}

func newHost(logw io.Writer, idle time.Duration) *host {
	h := &host{
		sessions: make(map[string]managedSession),
		newSession: func(name, program, workDir string, cols, rows int, autoYes bool) managedSession {
			return newConptySession(name, program, workDir, "cmd", cols, rows, autoYes)
		},
		lastActive:  time.Now(),
		idleTimeout: idle,
		logger:      log.New(logw, "[host] ", log.LstdFlags|log.Lmicroseconds),
		shutdownCh:  make(chan struct{}),
	}
	h.runs = newRunManager()
	h.workspaces = newWorkspaceManager(h)
	return h
}

func sizeOr(v, def int) int {
	if v <= 0 {
		return def
	}
	return v
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
	defer recoverGoroutine("host.handleConn")
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
		resp := h.safeDispatch(req)
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

// safeDispatch runs dispatch with panic recovery so a single bad request (e.g.
// from a stale/old-protocol client) can never crash the daemon and take every
// live session down with it. Recovered panics are logged to host.log.
func (h *host) safeDispatch(req *proto.Request) (resp *proto.Response) {
	defer func() {
		if r := recover(); r != nil {
			h.logger.Printf("PANIC handling method %q: %v\n%s", req.Method, r, debug.Stack())
			resp = &proto.Response{ID: req.ID, OK: false, Error: "internal host error"}
		}
	}()
	return h.dispatch(req)
}

// recoverGoroutine recovers a panic in a background goroutine and logs it
// instead of letting it crash the whole daemon process (which would kill every
// live session). Defer it as the first statement of a goroutine body.
func recoverGoroutine(where string) {
	if r := recover(); r != nil {
		if cslog.ErrorLog != nil {
			cslog.ErrorLog.Printf("winhost: recovered panic in %s: %v\n%s", where, r, debug.Stack())
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
			r.Alive = s.alive()
		}
		return r
	case proto.MethodListSessions:
		return &proto.Response{ID: req.ID, OK: true, Sessions: h.listSessions()}
	case proto.MethodCapturePane:
		s, ok := h.getSession(req.Session)
		if !ok {
			return proto.Errorf(req.ID, "no such session: %s", req.Session)
		}
		return &proto.Response{ID: req.ID, OK: true, Content: s.capture(req.Mode == proto.CaptureFull, req.WithANSI)}
	case proto.MethodSendKeys:
		s, ok := h.getSession(req.Session)
		if !ok {
			return proto.Errorf(req.ID, "no such session: %s", req.Session)
		}
		if err := s.sendKeys(req.Data); err != nil {
			return proto.Errorf(req.ID, "send keys: %v", err)
		}
		return &proto.Response{ID: req.ID, OK: true}
	case proto.MethodResize:
		s, ok := h.getSession(req.Session)
		if !ok {
			return proto.Errorf(req.ID, "no such session: %s", req.Session)
		}
		if err := s.resize(req.Cols, req.Rows); err != nil {
			return proto.Errorf(req.ID, "resize: %v", err)
		}
		return &proto.Response{ID: req.ID, OK: true}
	case proto.MethodHasUpdated:
		s, ok := h.getSession(req.Session)
		if !ok {
			return proto.Errorf(req.ID, "no such session: %s", req.Session)
		}
		u, p := s.hasUpdated()
		return &proto.Response{ID: req.ID, OK: true, Updated: u, HasPrompt: p}
	case proto.MethodSetAutoYes:
		s, ok := h.getSession(req.Session)
		if !ok {
			return proto.Errorf(req.ID, "no such session: %s", req.Session)
		}
		s.setAutoYes(req.Enabled)
		return &proto.Response{ID: req.ID, OK: true}
	case proto.MethodKillSession:
		h.killSession(req.Session)
		return &proto.Response{ID: req.ID, OK: true}
	case proto.MethodAttach:
		return h.attachSession(req)
	case proto.MethodShutdown:
		return &proto.Response{ID: req.ID, OK: true}
	case proto.MethodListWorkspaces:
		return h.workspaces.list(req)
	case proto.MethodCreateWorkspace:
		return h.workspaces.create(req)
	case proto.MethodGetWorkspace:
		return h.workspaces.get(req)
	case proto.MethodArchiveWorkspace:
		return h.workspaces.archive(req)
	case proto.MethodWorkspaceDiff:
		return h.workspaces.diff(req)
	case proto.MethodWorkspaceCommit:
		return h.workspaces.commit(req)
	case proto.MethodWorkspacePush:
		return h.workspaces.push(req)
	case proto.MethodSetWorkspaceAutoYes:
		return h.workspaces.setAutoYes(req)
	case proto.MethodStartRun:
		return h.workspaces.startRun(req)
	case proto.MethodStopRun:
		return h.workspaces.stopRun(req)
	case proto.MethodWorkspaceRunOutput:
		return h.workspaces.runOutput(req)
	case proto.MethodGenerateWorkspaceTitle:
		return h.workspaces.generateTitle(req)
	case proto.MethodRegenerateAgent:
		return h.workspaces.regenerate(req)
	case proto.MethodForceRegenerate:
		return h.workspaces.forceRegenerate(req)
	case proto.MethodUpdateWorkspace:
		return h.workspaces.updateWorkspace(req)
	case proto.MethodListCopilotSessions:
		return h.workspaces.listCopilotSessions(req)
	case proto.MethodResumeCopilotSession:
		return h.workspaces.resumeCopilotSession(req)
	default:
		return proto.Errorf(req.ID, "unknown method %q", req.Method)
	}
}

func (h *host) createSession(req *proto.Request) *proto.Response {
	if req.Session == "" {
		return proto.Errorf(req.ID, "session name required")
	}
	if req.Program == "" {
		return proto.Errorf(req.ID, "program required")
	}
	cols, rows := sizeOr(req.Cols, 80), sizeOr(req.Rows, 24)
	if err := h.startManagedSession(req.Session, req.Program, req.WorkDir, cols, rows, req.AutoYes); err != nil {
		return proto.Errorf(req.ID, "%v", err)
	}
	return &proto.Response{ID: req.ID, OK: true}
}

// startManagedSession creates, starts and registers a ConPTY session. It is the
// shared path used by both the CreateSession RPC and the workspace manager (so
// workspaces own their agent terminal directly, not via an RPC-to-self).
func (h *host) startManagedSession(name, program, workDir string, cols, rows int, autoYes bool) error {
	return h.startManagedSessionWithShell(name, program, workDir, "cmd", cols, rows, autoYes)
}

// startManagedSessionWithShell is like startManagedSession but accepts a shell
// parameter ("cmd", "powershell", "pwsh") controlling how the program is launched.
func (h *host) startManagedSessionWithShell(name, program, workDir, shell string, cols, rows int, autoYes bool) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, exists := h.sessions[name]; exists {
		return fmt.Errorf("session already exists: %s", name)
	}
	s := newConptySession(name, program, workDir, shell, cols, rows, autoYes)
	if err := s.start(); err != nil {
		return fmt.Errorf("start session: %w", err)
	}
	h.sessions[name] = s
	h.lastActive = time.Now()
	h.logger.Printf("created session %q (program=%q shell=%q cols=%d rows=%d)", name, program, shell, cols, rows)
	return nil
}

func (h *host) getSession(name string) (managedSession, bool) {
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
		out = append(out, s.info())
	}
	return out
}

func (h *host) killSession(name string) {
	h.mu.Lock()
	s, ok := h.sessions[name]
	if ok {
		delete(h.sessions, name)
	}
	h.mu.Unlock()
	if ok {
		_ = s.close()
		h.logger.Printf("killed session %q", name)
	}
}

// attachSession sets up a dedicated, token-guarded attach pipe for a session and
// returns its name + token to the client. A goroutine accepts one connection and
// streams output/input until the client detaches (closes the pipe).
func (h *host) attachSession(req *proto.Request) *proto.Response {
	sess, ok := h.getSession(req.Session)
	if !ok {
		// The session may belong to a workspace whose agent isn't running yet
		// (e.g. after a daemon restart). Revive it from persisted metadata, then
		// re-check, before giving up.
		revived, rerr := h.workspaces.reviveBySession(req.Session, req.Cols, req.Rows)
		if rerr != nil {
			return proto.Errorf(req.ID, "revive workspace session: %v", rerr)
		}
		if revived {
			sess, ok = h.getSession(req.Session)
		}
		if !ok {
			return proto.Errorf(req.ID, "no such session: %s", req.Session)
		}
	}
	sid, err := currentUserSID()
	if err != nil {
		return proto.Errorf(req.ID, "%v", err)
	}
	sddl, err := currentUserSDDL()
	if err != nil {
		return proto.Errorf(req.ID, "%v", err)
	}
	seq := h.attachSeq.Add(1)
	pipe := fmt.Sprintf(`\\.\pipe\hangar-att-%s-%s-%d`, sid, req.Session, seq)
	ln, err := winio.ListenPipe(pipe, &winio.PipeConfig{SecurityDescriptor: sddl})
	if err != nil {
		return proto.Errorf(req.ID, "attach listen: %v", err)
	}
	token := randomNonce()
	go h.runAttach(sess, ln, token, req.Cols, req.Rows)
	return &proto.Response{ID: req.ID, OK: true, AttachPipe: pipe, AttachToken: token}
}

func (h *host) runAttach(sess managedSession, ln net.Listener, token string, cols, rows int) {
	defer ln.Close()
	defer recoverGoroutine("host.runAttach")

	// Watchdog: if no client connects shortly, tear down the pipe.
	connected := make(chan struct{})
	go func() {
		select {
		case <-connected:
		case <-time.After(10 * time.Second):
			_ = ln.Close()
		case <-h.shutdownCh:
			_ = ln.Close()
		}
	}()

	conn, err := ln.Accept()
	close(connected)
	if err != nil {
		return
	}
	defer conn.Close()

	// First frame must be the auth token.
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	tok, err := proto.ReadFrameBytes(conn)
	_ = conn.SetReadDeadline(time.Time{})
	if err != nil || string(tok) != token {
		return
	}

	snapshot, sub := sess.subscribe(cols, rows)
	defer sess.unsubscribe(sub)

	if _, err := conn.Write(snapshot); err != nil {
		return
	}

	// Output: stream live bytes to the client.
	go func() {
		defer recoverGoroutine("host.runAttach.output")
		for b := range sub.ch {
			if _, err := conn.Write(b); err != nil {
				return
			}
		}
	}()

	// Input: forward the client's keystrokes to the child until it detaches.
	buf := make([]byte, 4096)
	for {
		n, rerr := conn.Read(buf)
		if n > 0 {
			_ = sess.sendKeys(append([]byte(nil), buf[:n]...))
		}
		if rerr != nil {
			return
		}
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
		// Tear down any live sessions so no child processes are orphaned.
		h.mu.Lock()
		sessions := make([]managedSession, 0, len(h.sessions))
		for _, s := range h.sessions {
			sessions = append(sessions, s)
		}
		h.sessions = make(map[string]managedSession)
		h.mu.Unlock()
		for _, s := range sessions {
			_ = s.close()
		}
		if h.runs != nil {
			h.runs.stopAll()
		}
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

	// Initialize the global loggers: the host now drives config/git (workspace
	// management), which log via this package and would otherwise nil-panic.
	cslog.Initialize(true)
	defer cslog.Close()

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
