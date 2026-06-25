//go:build windows

package winhost

import (
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"time"

	cslog "hangar/log"
	"hangar/session/agentcmd"
	"hangar/session/winhost/proto"

	"github.com/Microsoft/go-winio"
)

// defaultIdleTimeout: the host self-exits after this long with zero sessions and
// zero connected clients (tmux-server-like, without lingering forever).
const defaultIdleTimeout = 5 * time.Minute

// slowDispatchThreshold: handlers taking at least this long are logged, since a
// connection's RPCs are processed serially and a slow one delays all the others.
const slowDispatchThreshold = 750 * time.Millisecond

// bodyReadTimeout bounds how long the host waits for a frame body once its
// header has arrived (anti-stuck-client). The header read itself is bounded by
// headerReadTimeout to prevent abandoned connections from parking goroutines.
const bodyReadTimeout = 15 * time.Second

// headerReadTimeout bounds how long we wait for the first byte of a new request
// header on a persistent control connection.  Set longer than bodyReadTimeout to
// allow genuine idle periods between RPCs, but finite to prevent an abandoned
// same-user pipe connection from inflating activeConns indefinitely (HARDEN-22).
const headerReadTimeout = 5 * time.Minute

// managedSession is the host's view of a session. The real implementation is
// conptySession (ConPTY + VT emulator); tests inject a fake via host.newSession.
type managedSession interface {
	start() error
	capture(full, withANSI bool) string
	captureHistory(includeScreen bool, cols, rows int) (string, bool, int)
	sendKeys(b []byte) error
	resize(cols, rows int) error
	hasUpdated() (updated, hasPrompt bool)
	agentStatus() (busy, waiting bool)
	bracketedPasteEnabled() bool
	lastOutputUnixMs() int64
	setAutoYes(enabled bool)
	armTrustApproval(reason string, expiresAt time.Time)
	clearTrustApproval()
	info() proto.SessionInfo
	alive() bool
	close() error
	subscribe(cols, rows int) (snapshot []byte, sub *subscriber)
	unsubscribe(sub *subscriber)
}

type host struct {
	mu          sync.RWMutex
	sessions    map[string]managedSession
	newSession  func(name, program, workDir, shell string, cols, rows int, autoYes bool, logger *log.Logger) managedSession
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
	identity   *hostIdentity
}

func newHost(logw io.Writer, idle time.Duration) *host {
	h := &host{
		sessions: make(map[string]managedSession),
		newSession: func(name, program, workDir, shell string, cols, rows int, autoYes bool, logger *log.Logger) managedSession {
			return newConptySession(name, program, workDir, shell, cols, rows, autoYes, logger)
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
	authenticated := false
	for {
		// Apply a header-read deadline so an idle or abandoned connection cannot
		// park the goroutine and inflate activeConns indefinitely (HARDEN-22).
		_ = conn.SetReadDeadline(time.Now().Add(headerReadTimeout))
		n, err := proto.ReadHeader(conn)
		_ = conn.SetReadDeadline(time.Time{})
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
		var resp *proto.Response
		if !authenticated && req.Method != proto.MethodHello {
			resp = proto.Errorf(req.ID, "authenticated Hello required")
		} else {
			dispatchStart := time.Now()
			resp = h.safeDispatch(req)
			// A connection's requests are handled serially, so a slow handler
			// delays every later RPC on that pipe (which can cascade into client
			// timeouts and blank panes during rapid workspace/shell switching).
			// Log slow handlers so the offending method is visible in host.log.
			if d := time.Since(dispatchStart); d >= slowDispatchThreshold {
				h.logger.Printf("slow dispatch method=%s took=%s session=%q workspace=%q", req.Method, d.Round(time.Millisecond), req.Session, req.WorkspaceID)
			}
			if req.Method == proto.MethodHello && resp.OK {
				authenticated = true
			}
		}
		_ = conn.SetWriteDeadline(time.Now().Add(30 * time.Second))
		if err := proto.WriteFrame(conn, resp); err != nil {
			return
		}
		_ = conn.SetWriteDeadline(time.Time{})
		if req.Method == proto.MethodShutdown && resp.OK {
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
		if req.ClientVersion != proto.Version {
			return proto.Errorf(req.ID, "protocol mismatch: host=%d client=%d", proto.Version, req.ClientVersion)
		}
		if !validLowerHexBytes(req.ClientNonce, 32) {
			return proto.Errorf(req.ID, "missing or invalid hello challenge")
		}
		if h.identity == nil {
			return proto.Errorf(req.ID, "host identity unavailable")
		}
		proof, err := hostNonceProofForIdentity(h.identity, req.ClientNonce)
		if err != nil {
			return proto.Errorf(req.ID, "host authentication unavailable")
		}
		return &proto.Response{
			ID:              req.ID,
			OK:              true,
			HostVersion:     h.identity.Version,
			HostPID:         h.identity.PID,
			HostCreatedUnix: h.identity.CreatedUnix,
			HostNonceProof:  proof,
		}
	case proto.MethodCreateSession:
		return h.createSession(req)
	case proto.MethodHasSession:
		s, ok := h.getSession(req.Session)
		r := &proto.Response{ID: req.ID, OK: true, Exists: ok}
		if ok {
			r.Alive = s.alive()
			r.ExitCode = s.info().ExitCode
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
	case proto.MethodCaptureHistory:
		s, ok := h.getSession(req.Session)
		if !ok {
			return proto.Errorf(req.ID, "no such session: %s", req.Session)
		}
		ansi, altScreen, lines := s.captureHistory(req.IncludeScreen, req.Cols, req.Rows)
		return &proto.Response{ID: req.ID, OK: true, Content: ansi, AltScreen: altScreen, ScrollbackLines: lines}
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
	case proto.MethodOpenRichStream:
		return h.openRichStream(req)
	case proto.MethodSendMessage:
		return h.sendRichMessage(req)
	case proto.MethodAbortTurn:
		return h.abortRichTurn(req)
	case proto.MethodGetTranscript:
		return h.getRichTranscript(req)
	case proto.MethodRespondPermission:
		return h.respondRichPermission(req)
	case proto.MethodRespondUserInput:
		return h.respondRichUserInput(req)
	case proto.MethodListModels:
		return h.listRichModels(req)
	case proto.MethodSetModel:
		return h.setRichModel(req)
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
	s := h.newSession(name, program, workDir, shell, cols, rows, autoYes, h.logger)
	if err := s.start(); err != nil {
		return fmt.Errorf("start session: %w", err)
	}
	h.sessions[name] = s
	h.lastActive = time.Now()
	programName, argCount := safeProgramSummary(program, shell)
	h.logger.Printf("created session %q (programName=%q argCount=%d shell=%q cols=%d rows=%d)", name, programName, argCount, shell, cols, rows)
	return nil
}

// startSDKSession creates, starts/resumes and registers a Copilot SDK-backed
// "rich" session (the opt-in structured backend, parallel to
// startManagedSessionWithShell). sessionID seeds or resumes the SDK session id so
// a later resume continues the same conversation; baseDir overrides COPILOT_HOME
// ("" = default). model/effort/contextTier (v18) seed the SDK model selection so a
// revived session restores the user's choice ("" = a fresh chat / the SDK default).
func (h *host) startSDKSession(name, program, workDir, baseDir string, autoYes bool, sessionID, model, effort, contextTier string, resume bool) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, exists := h.sessions[name]; exists {
		return fmt.Errorf("session already exists: %s", name)
	}
	s := newSDKSession(name, program, workDir, baseDir, autoYes, sessionID, model, effort, contextTier, nil, h.logger)
	var err error
	if resume {
		err = s.startResumed()
	} else {
		err = s.start()
	}
	if err != nil {
		return fmt.Errorf("start sdk session: %w", err)
	}
	// Emit the active model after start/resume (and after any transcript replay) so a
	// (re)attaching desktop restores the model selector (v18). A no-op for a fresh
	// chat with no selection yet.
	s.emitModelFrame()
	h.sessions[name] = s
	h.lastActive = time.Now()
	h.logger.Printf("created SDK (rich) session %q program=%q workDir=%q resume=%v", name, filepath.Base(program), workDir, resume)
	return nil
}

func safeProgramSummary(program, shell string) (programName string, argCount int) {
	switch agentcmd.ParseShellKind(shell) {
	case agentcmd.ShellPowerShell, agentcmd.ShellPwsh:
		return "<shell-script>", 0
	default:
		argv, err := agentcmd.ParseProgram(program)
		if err != nil || len(argv) == 0 {
			return "<invalid>", 0
		}
		return filepath.Base(argv[0]), len(argv) - 1
	}
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
			h.logger.Printf("attach revive failed session=%q err=%v", req.Session, rerr)
			return proto.Errorf(req.ID, "revive workspace session: %v", rerr)
		}
		if revived {
			h.logger.Printf("attach revived workspace session=%q", req.Session)
			sess, ok = h.getSession(req.Session)
		}
		if !ok {
			h.logger.Printf("attach failed session=%q err=no-such-session", req.Session)
			return proto.Errorf(req.ID, "no such session: %s", req.Session)
		}
	}
	sid, err := currentUserSID()
	if err != nil {
		h.logger.Printf("attach current user sid failed session=%q err=%v", req.Session, err)
		return proto.Errorf(req.ID, "%v", err)
	}
	sddl, err := currentUserSDDL()
	if err != nil {
		h.logger.Printf("attach current user sddl failed session=%q err=%v", req.Session, err)
		return proto.Errorf(req.ID, "%v", err)
	}
	seq := h.attachSeq.Add(1)
	pipe := fmt.Sprintf(`\\.\pipe\hangar-att-%s-%s-%d`, sid, req.Session, seq)
	ln, err := winio.ListenPipe(pipe, &winio.PipeConfig{SecurityDescriptor: sddl})
	if err != nil {
		h.logger.Printf("attach listen failed session=%q seq=%d err=%v", req.Session, seq, err)
		return proto.Errorf(req.ID, "attach listen: %v", err)
	}
	token, err := randomNonceHex(16)
	if err != nil {
		_ = ln.Close()
		h.logger.Printf("attach token failed session=%q seq=%d err=%v", req.Session, seq, err)
		return proto.Errorf(req.ID, "attach token: %v", err)
	}
	alive := sess.alive()
	info := sess.info()
	h.logger.Printf("attach pipe created session=%q seq=%d pipe=%q alive=%v", req.Session, seq, pipe, alive)
	go h.runAttach(sess, ln, token, req.Cols, req.Rows)
	return &proto.Response{ID: req.ID, OK: true, AttachPipe: pipe, AttachToken: token, Alive: alive, ExitCode: info.ExitCode}
}

func (h *host) richSession(req *proto.Request) (*sdkSession, *proto.Response) {
	sess, ok := h.getSession(req.Session)
	if !ok {
		return nil, proto.Errorf(req.ID, "no such session: %s", req.Session)
	}
	rich, ok := sess.(*sdkSession)
	if !ok {
		return nil, proto.Errorf(req.ID, "session %q is not a rich session", req.Session)
	}
	return rich, nil
}

func (h *host) openRichStream(req *proto.Request) *proto.Response {
	revived, rerr := h.workspaces.reviveBySession(req.Session, req.Cols, req.Rows)
	if rerr != nil {
		h.logger.Printf("rich stream revive failed session=%q err=%v", req.Session, rerr)
		return proto.Errorf(req.ID, "revive workspace session: %v", rerr)
	}
	if revived {
		h.logger.Printf("rich stream revived workspace session=%q", req.Session)
	}
	sess, errResp := h.richSession(req)
	if errResp != nil {
		return errResp
	}
	sid, err := currentUserSID()
	if err != nil {
		h.logger.Printf("rich stream current user sid failed session=%q err=%v", req.Session, err)
		return proto.Errorf(req.ID, "%v", err)
	}
	sddl, err := currentUserSDDL()
	if err != nil {
		h.logger.Printf("rich stream current user sddl failed session=%q err=%v", req.Session, err)
		return proto.Errorf(req.ID, "%v", err)
	}
	seq := h.attachSeq.Add(1)
	pipe := fmt.Sprintf(`\\.\pipe\hangar-rich-%s-%s-%d`, sid, req.Session, seq)
	ln, err := winio.ListenPipe(pipe, &winio.PipeConfig{SecurityDescriptor: sddl})
	if err != nil {
		h.logger.Printf("rich stream listen failed session=%q seq=%d err=%v", req.Session, seq, err)
		return proto.Errorf(req.ID, "rich stream listen: %v", err)
	}
	token, err := randomNonceHex(16)
	if err != nil {
		_ = ln.Close()
		h.logger.Printf("rich stream token failed session=%q seq=%d err=%v", req.Session, seq, err)
		return proto.Errorf(req.ID, "rich stream token: %v", err)
	}
	alive := sess.alive()
	info := sess.info()
	h.logger.Printf("rich stream pipe created session=%q seq=%d pipe=%q alive=%v", req.Session, seq, pipe, alive)
	go h.runRichStream(sess, ln, token, req.Since)
	return &proto.Response{ID: req.ID, OK: true, AttachPipe: pipe, AttachToken: token, Alive: alive, ExitCode: info.ExitCode}
}

func (h *host) sendRichMessage(req *proto.Request) *proto.Response {
	sess, errResp := h.richSession(req)
	if errResp != nil {
		return errResp
	}
	go func() {
		defer recoverGoroutine("host.sendRichMessage")
		if err := sess.richSend(context.Background(), req.Message, req.Attachments); err != nil {
			h.logger.Printf("rich send failed session=%q err=%v", req.Session, err)
		}
	}()
	return &proto.Response{ID: req.ID, OK: true}
}

func (h *host) abortRichTurn(req *proto.Request) *proto.Response {
	sess, errResp := h.richSession(req)
	if errResp != nil {
		return errResp
	}
	if err := sess.richAbort(context.Background()); err != nil {
		return proto.Errorf(req.ID, "abort rich turn: %v", err)
	}
	return &proto.Response{ID: req.ID, OK: true}
}

func (h *host) getRichTranscript(req *proto.Request) *proto.Response {
	sess, errResp := h.richSession(req)
	if errResp != nil {
		return errResp
	}
	return &proto.Response{ID: req.ID, OK: true, Frames: sess.richTranscript(req.Since)}
}

func (h *host) respondRichPermission(req *proto.Request) *proto.Response {
	sess, errResp := h.richSession(req)
	if errResp != nil {
		return errResp
	}
	if err := sess.richRespondPermission(context.Background(), req.RequestID, req.Decision == proto.DecisionApprove); err != nil {
		return proto.Errorf(req.ID, "respond permission: %v", err)
	}
	return &proto.Response{ID: req.ID, OK: true}
}

func (h *host) respondRichUserInput(req *proto.Request) *proto.Response {
	sess, errResp := h.richSession(req)
	if errResp != nil {
		return errResp
	}
	if err := sess.richRespondUserInput(req.RequestID, req.Answer, req.Freeform); err != nil {
		return proto.Errorf(req.ID, "respond user input: %v", err)
	}
	return &proto.Response{ID: req.ID, OK: true}
}

func (h *host) listRichModels(req *proto.Request) *proto.Response {
	sess, errResp := h.richSession(req)
	if errResp != nil {
		return errResp
	}
	models, err := sess.richListModels(context.Background())
	if err != nil {
		return proto.Errorf(req.ID, "list models: %v", err)
	}
	return &proto.Response{ID: req.ID, OK: true, Models: models}
}

func (h *host) setRichModel(req *proto.Request) *proto.Response {
	sess, errResp := h.richSession(req)
	if errResp != nil {
		return errResp
	}
	if err := sess.richSetModel(context.Background(), req.Model, req.Effort, req.ContextTier); err != nil {
		return proto.Errorf(req.ID, "set model: %v", err)
	}
	// Persist the selection so a later revive restores it (v18): without this the
	// model is blank after a daemon restart. Keyed by session name like the switch.
	h.workspaces.setRichModelSelection(req.Session, req.Model, req.Effort, req.ContextTier)
	return &proto.Response{ID: req.ID, OK: true}
}

func (h *host) runRichStream(sess *sdkSession, ln net.Listener, token string, since uint64) {
	defer ln.Close()
	defer recoverGoroutine("host.runRichStream")
	info := sess.info()
	sessionName := info.Name
	reason := "completed"
	defer func() {
		h.logger.Printf("rich stream teardown session=%q reason=%s", sessionName, reason)
	}()

	connected := make(chan struct{})
	go func() {
		select {
		case <-connected:
		case <-time.After(10 * time.Second):
			h.logger.Printf("rich stream watchdog teardown session=%q reason=no-client-timeout", sessionName)
			_ = ln.Close()
		case <-h.shutdownCh:
			h.logger.Printf("rich stream watchdog teardown session=%q reason=host-shutdown", sessionName)
			_ = ln.Close()
		}
	}()

	conn, err := ln.Accept()
	close(connected)
	if err != nil {
		reason = fmt.Sprintf("accept failed: %v", err)
		h.logger.Printf("rich stream accept failed session=%q err=%v", sessionName, err)
		return
	}
	h.logger.Printf("rich stream client connected session=%q remote=%q", sessionName, conn.RemoteAddr())
	defer conn.Close()

	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	tok, err := proto.ReadFrameBytes(conn)
	_ = conn.SetReadDeadline(time.Time{})
	if err != nil {
		reason = fmt.Sprintf("token read failed: %v", err)
		h.logger.Printf("rich stream token read failed session=%q err=%v", sessionName, err)
		return
	}
	if string(tok) != token {
		reason = "token mismatch"
		h.logger.Printf("rich stream token mismatch session=%q tokenBytes=%d", sessionName, len(tok))
		return
	}

	snapshot, sub := sess.richSubscribe(since)
	defer sess.richUnsubscribe(sub)

	for _, frame := range snapshot {
		_ = conn.SetWriteDeadline(time.Now().Add(30 * time.Second))
		err := proto.WriteFrame(conn, frame)
		_ = conn.SetWriteDeadline(time.Time{})
		if err != nil {
			reason = fmt.Sprintf("snapshot write failed: %v", err)
			h.logger.Printf("rich stream snapshot write failed session=%q seq=%d err=%v", sessionName, frame.Seq, err)
			return
		}
	}
	h.logger.Printf("rich stream snapshot written session=%q frames=%d", sessionName, len(snapshot))

	clientGone := make(chan struct{})
	go func() {
		defer recoverGoroutine("host.runRichStream.disconnect")
		var one [1]byte
		for {
			if _, err := conn.Read(one[:]); err != nil {
				close(clientGone)
				return
			}
		}
	}()

	for {
		select {
		case b, ok := <-sub.ch:
			if !ok {
				reason = "session-unsubscribed"
				return
			}
			_ = conn.SetWriteDeadline(time.Now().Add(30 * time.Second))
			err := proto.WriteRawFrame(conn, b)
			_ = conn.SetWriteDeadline(time.Time{})
			if err != nil {
				reason = fmt.Sprintf("stream write failed: %v", err)
				h.logger.Printf("rich stream output ended session=%q err=%v", sessionName, err)
				return
			}
		case <-clientGone:
			reason = "client detached"
			return
		case <-h.shutdownCh:
			reason = "host shutdown"
			return
		}
	}
}

func (h *host) runAttach(sess managedSession, ln net.Listener, token string, cols, rows int) {
	defer ln.Close()
	defer recoverGoroutine("host.runAttach")
	info := sess.info()
	sessionName := info.Name
	reason := "completed"
	defer func() {
		h.logger.Printf("attach teardown session=%q reason=%s", sessionName, reason)
	}()

	// Watchdog: if no client connects shortly, tear down the pipe.
	connected := make(chan struct{})
	go func() {
		select {
		case <-connected:
		case <-time.After(10 * time.Second):
			h.logger.Printf("attach watchdog teardown session=%q reason=no-client-timeout", sessionName)
			_ = ln.Close()
		case <-h.shutdownCh:
			h.logger.Printf("attach watchdog teardown session=%q reason=host-shutdown", sessionName)
			_ = ln.Close()
		}
	}()

	conn, err := ln.Accept()
	close(connected)
	if err != nil {
		reason = fmt.Sprintf("accept failed: %v", err)
		h.logger.Printf("attach accept failed session=%q err=%v", sessionName, err)
		return
	}
	h.logger.Printf("attach client connected session=%q remote=%q", sessionName, conn.RemoteAddr())
	defer conn.Close()

	// First frame must be the auth token.
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	tok, err := proto.ReadFrameBytes(conn)
	_ = conn.SetReadDeadline(time.Time{})
	if err != nil {
		reason = fmt.Sprintf("token read failed: %v", err)
		h.logger.Printf("attach token read failed session=%q err=%v", sessionName, err)
		return
	}
	if string(tok) != token {
		reason = "token mismatch"
		h.logger.Printf("attach token mismatch session=%q tokenBytes=%d", sessionName, len(tok))
		return
	}

	snapshot, sub := sess.subscribe(cols, rows)
	defer sess.unsubscribe(sub)

	if _, err := conn.Write(snapshot); err != nil {
		reason = fmt.Sprintf("snapshot write failed: %v", err)
		h.logger.Printf("attach snapshot write failed session=%q bytes=%d err=%v", sessionName, len(snapshot), err)
		return
	}
	h.logger.Printf("attach snapshot written session=%q bytes=%d", sessionName, len(snapshot))

	// Output: stream live bytes to the client.
	go func() {
		defer recoverGoroutine("host.runAttach.output")
		for b := range sub.ch {
			if _, err := conn.Write(b); err != nil {
				h.logger.Printf("attach output stream ended session=%q err=%v", sessionName, err)
				return
			}
		}
		h.logger.Printf("attach output stream ended session=%q reason=session-unsubscribed", sessionName)
	}()

	// Input: forward the client's keystrokes to the child until it detaches.
	buf := make([]byte, 4096)
	for {
		n, rerr := conn.Read(buf)
		if n > 0 {
			_ = sess.sendKeys(append([]byte(nil), buf[:n]...))
		}
		if rerr != nil {
			reason = fmt.Sprintf("client detached: %v", rerr)
			h.logger.Printf("attach input ended session=%q err=%v", sessionName, rerr)
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
	hostLog := ""
	hostLogOpened := false
	if lp, err := hostLogPath(); err == nil {
		hostLog = lp
		if f, err := os.OpenFile(lp, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600); err == nil {
			logw = f
			hostLogOpened = true
			defer f.Close()
		} else {
			if cslog.ErrorLog != nil {
				cslog.ErrorLog.Printf("winhost: open host.log %s: %v", lp, err)
			}
			fmt.Fprintf(os.Stderr, "winhost: open host.log %s: %v\n", lp, err)
		}
	} else {
		if cslog.ErrorLog != nil {
			cslog.ErrorLog.Printf("winhost: resolve host.log path: %v", err)
		}
		fmt.Fprintf(os.Stderr, "winhost: resolve host.log path: %v\n", err)
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
	if hostLogOpened {
		h.logger.Printf("host log path=%s", hostLog)
	}
	identity, err := newHostIdentity(pipe)
	if err != nil {
		_ = ln.Close()
		return err
	}
	h.identity = identity
	if err := writeHostInfo(identity); err != nil {
		_ = ln.Close()
		return fmt.Errorf("write host.json: %w", err)
	}
	defer removeHostInfo()

	go h.idleLoop()
	h.workspaces.startDiffRefresh()
	h.logger.Printf("session-host started pid=%d pipe=%s version=%d", os.Getpid(), pipe, proto.Version)
	h.serve(ln) // blocks until shutdown
	h.logger.Printf("session-host stopped")
	return nil
}
