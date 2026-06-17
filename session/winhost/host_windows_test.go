//go:build windows

package winhost

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"claude-squad/session/winhost/proto"

	"github.com/Microsoft/go-winio"
)

// fakeSession is an in-memory managedSession used to test host dispatch and
// lifecycle without spawning real ConPTY processes.
type fakeSession struct {
	name, program string
	workDir       string
	mu            sync.Mutex
	buf           []byte
	changed       bool
	autoYes       bool
	trustApproval bool
	aliveFlag     bool
	busy          bool
	waiting       bool
	sendHook      func(*fakeSession, []byte) error
	failStart     bool
	startErr      error
}

func newFake(name, program, workDir, shell string, cols, rows int, autoYes bool) managedSession {
	return &fakeSession{
		name: name, program: program, workDir: workDir, autoYes: autoYes, aliveFlag: true,
		buf: []byte(fmt.Sprintf("[echo session %q running %q]\n", name, program)),
	}
}

// conptyFunctional probes once whether real ConPTY sessions actually produce
// output in this environment. Headless/CI runners cannot drive a real console,
// where ConPTY reads block indefinitely, so real-console tests are skipped there
// instead of hanging. The probe is bounded and cached for the whole package run.
var conptyFunctional = sync.OnceValue(func() bool {
	done := make(chan bool, 1)
	go func() {
		sess, ok := newConptySession("conpty-probe", `cmd.exe /c echo HANGAR_CONPTY_PROBE`, "", "cmd", 80, 24, false).(*conptySession)
		if !ok {
			done <- false
			return
		}
		if err := sess.start(); err != nil {
			done <- false
			return
		}
		defer sess.close()
		for i := 0; i < 40; i++ {
			if strings.Contains(sess.capture(true, false), "HANGAR_CONPTY_PROBE") {
				done <- true
				return
			}
			time.Sleep(100 * time.Millisecond)
		}
		done <- false
	}()
	select {
	case ok := <-done:
		return ok
	case <-time.After(8 * time.Second):
		return false
	}
})

// requireConPTY skips a test that depends on a real, functional ConPTY console
// (process I/O) when none is available, e.g. a headless CI runner.
func requireConPTY(t *testing.T) {
	t.Helper()
	if !conptyFunctional() {
		t.Skip("ConPTY not functional in this environment (headless); skipping real-console test")
	}
}

func (f *fakeSession) start() error {
	if f.failStart {
		if f.startErr != nil {
			return f.startErr
		}
		return fmt.Errorf("fake start failure")
	}
	return nil
}
func (f *fakeSession) capture(full, withANSI bool) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return string(f.buf)
}
func (f *fakeSession) sendKeys(b []byte) error {
	f.mu.Lock()
	f.buf = append(f.buf, b...)
	f.changed = true
	hook := f.sendHook
	f.mu.Unlock()
	if hook != nil {
		return hook(f, b)
	}
	return nil
}
func (f *fakeSession) resize(cols, rows int) error { return nil }
func (f *fakeSession) hasUpdated() (bool, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	u := f.changed
	f.changed = false
	return u, false
}
func (f *fakeSession) agentStatus() (bool, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.busy, f.waiting
}
func (f *fakeSession) setAutoYes(e bool) { f.mu.Lock(); f.autoYes = e; f.mu.Unlock() }
func (f *fakeSession) armTrustApproval(reason string, expiresAt time.Time) {
	f.mu.Lock()
	f.trustApproval = true
	f.mu.Unlock()
}
func (f *fakeSession) clearTrustApproval() {
	f.mu.Lock()
	f.trustApproval = false
	f.mu.Unlock()
}
func (f *fakeSession) info() proto.SessionInfo {
	f.mu.Lock()
	defer f.mu.Unlock()
	return proto.SessionInfo{Name: f.name, Alive: f.aliveFlag, Program: f.program}
}
func (f *fakeSession) alive() bool  { f.mu.Lock(); defer f.mu.Unlock(); return f.aliveFlag }
func (f *fakeSession) close() error { f.mu.Lock(); f.aliveFlag = false; f.mu.Unlock(); return nil }
func (f *fakeSession) subscribe(cols, rows int) ([]byte, *subscriber) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]byte(nil), f.buf...), &subscriber{ch: make(chan []byte, 16)}
}
func (f *fakeSession) unsubscribe(sub *subscriber) { close(sub.ch) }

// startTestHost starts an in-process host on a unique pipe using the fake
// session factory (no real processes spawned).
func startTestHost(t *testing.T) (string, func()) {
	pipe, _, cleanup := startTestHostWithHandle(t)
	return pipe, cleanup
}

func startTestHostWithHandle(t *testing.T) (string, *host, func()) {
	t.Helper()
	pipe := fmt.Sprintf(`\\.\pipe\claudesquad-test-%d-%d`, os.Getpid(), time.Now().UnixNano())
	sddl, err := currentUserSDDL()
	if err != nil {
		t.Fatalf("sddl: %v", err)
	}
	ln, err := winio.ListenPipe(pipe, &winio.PipeConfig{SecurityDescriptor: sddl})
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	h := newHost(io.Discard, time.Minute)
	nonce, err := randomNonceHex(32)
	if err != nil {
		t.Fatalf("nonce: %v", err)
	}
	h.identity = &hostIdentity{
		PipeName:    pipe,
		PID:         os.Getpid(),
		CreatedUnix: time.Now().Unix(),
		Nonce:       nonce,
		Version:     proto.Version,
	}
	h.newSession = newFake
	h.workspaces.bootFloor = 0   // no boot floor in tests
	h.workspaces.submitDelay = 0 // no submit settle delay in tests
	go h.serve(ln)
	return pipe, h, func() { h.triggerShutdown() }
}

// startRealHost starts an in-process host using the real ConPTY session factory.
func startRealHost(t *testing.T) (string, func()) {
	t.Helper()
	pipe := fmt.Sprintf(`\\.\pipe\claudesquad-rtest-%d-%d`, os.Getpid(), time.Now().UnixNano())
	sddl, err := currentUserSDDL()
	if err != nil {
		t.Fatalf("sddl: %v", err)
	}
	ln, err := winio.ListenPipe(pipe, &winio.PipeConfig{SecurityDescriptor: sddl})
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	h := newHost(io.Discard, time.Minute)
	nonce, err := randomNonceHex(32)
	if err != nil {
		t.Fatalf("nonce: %v", err)
	}
	h.identity = &hostIdentity{
		PipeName:    pipe,
		PID:         os.Getpid(),
		CreatedUnix: time.Now().Unix(),
		Nonce:       nonce,
		Version:     proto.Version,
	}
	go h.serve(ln)
	return pipe, func() { h.triggerShutdown() }
}

func authClient(t *testing.T, c *Client) {
	t.Helper()
	r, err := c.Hello()
	if err != nil || !r.OK || r.HostVersion != proto.Version {
		t.Fatalf("hello: resp=%+v err=%v", r, err)
	}
}

func TestHostLifecycle(t *testing.T) {
	pipe, cleanup := startTestHost(t)
	defer cleanup()

	c, err := dialClient(pipe, 3*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer c.Close()

	authClient(t, c)

	if err := c.CreateSession("s1", "copilot", `C:\tmp`, 80, 24, false); err != nil {
		t.Fatalf("create: %v", err)
	}

	exists, alive, err := c.HasSession("s1")
	if err != nil || !exists || !alive {
		t.Fatalf("has: exists=%v alive=%v err=%v", exists, alive, err)
	}

	out, err := c.CapturePane("s1", proto.CaptureScreen, true)
	if err != nil || !strings.Contains(out, "echo session") {
		t.Fatalf("capture banner: out=%q err=%v", out, err)
	}

	if err := c.SendKeys("s1", []byte("hello-world")); err != nil {
		t.Fatalf("sendkeys: %v", err)
	}
	out, _ = c.CapturePane("s1", proto.CaptureScreen, true)
	if !strings.Contains(out, "hello-world") {
		t.Fatalf("after sendkeys capture: %q", out)
	}

	if u, _, _ := c.HasUpdated("s1"); !u {
		t.Fatal("expected updated=true after sendkeys")
	}
	if u, _, _ := c.HasUpdated("s1"); u {
		t.Fatal("expected updated=false on second poll")
	}

	if err := c.SetAutoYes("s1", true); err != nil {
		t.Fatalf("setautoyes: %v", err)
	}

	if err := c.CreateSession("s1", "x", "", 80, 24, false); err == nil {
		t.Fatal("expected duplicate-session error")
	}

	if ls, err := c.ListSessions(); err != nil || len(ls) != 1 {
		t.Fatalf("list: n=%d err=%v", len(ls), err)
	}

	if _, err := c.CapturePane("nope", proto.CaptureScreen, true); err == nil {
		t.Fatal("expected error capturing nonexistent session")
	}

	if err := c.Kill("s1"); err != nil {
		t.Fatalf("kill: %v", err)
	}
	if exists, _, _ := c.HasSession("s1"); exists {
		t.Fatal("expected session gone after kill")
	}
}

func TestHostShutdownStopsServe(t *testing.T) {
	pipe, _ := startTestHost(t)

	c, err := dialClient(pipe, 3*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	authClient(t, c)
	if err := c.Shutdown(); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	c.Close()

	time.Sleep(300 * time.Millisecond)
	if c2, err := dialClient(pipe, 500*time.Millisecond); err == nil {
		c2.Close()
		t.Fatal("expected dial to fail after shutdown")
	}
}

func TestConcurrentClients(t *testing.T) {
	pipe, cleanup := startTestHost(t)
	defer cleanup()

	c1, err := dialClient(pipe, 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer c1.Close()
	authClient(t, c1)
	c2, err := dialClient(pipe, 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer c2.Close()
	authClient(t, c2)

	if err := c1.CreateSession("a", "copilot", "", 80, 24, false); err != nil {
		t.Fatal(err)
	}
	if err := c2.CreateSession("b", "claude", "", 80, 24, false); err != nil {
		t.Fatal(err)
	}
	ls, err := c1.ListSessions()
	if err != nil || len(ls) != 2 {
		t.Fatalf("expected 2 sessions, got %d err=%v", len(ls), err)
	}
}

// TestConptySessionRealEcho exercises the real ConPTY + VT emulator path with a
// short-lived command, verifying the rendered screen captures the program's
// output and the exit is recorded.
func TestConptySessionRealEcho(t *testing.T) {
	requireConPTY(t)
	s := newConptySession("t", `cmd.exe /c echo P2_CONPTY_OK`, "", "cmd", 80, 24, false).(*conptySession)
	if err := s.start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer s.close()

	deadline := time.Now().Add(15 * time.Second)
	for s.alive() && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if s.alive() {
		t.Fatal("child did not exit in time")
	}
	// Wait for the drain goroutine to flush the final output into the emulator.
	select {
	case <-s.drainDone:
	case <-time.After(3 * time.Second):
		t.Fatal("drain did not finish")
	}

	out := s.capture(false, false)
	if !strings.Contains(out, "P2_CONPTY_OK") {
		t.Fatalf("expected echoed text in capture, got %q", out)
	}
	if info := s.info(); info.ExitCode != 0 {
		t.Fatalf("expected exit code 0, got %d", info.ExitCode)
	}
}

// TestConptySessionResizeAndAnsi verifies resize and that ANSI capture preserves
// styling while plain capture does not leak escape sequences.
func TestConptySessionResizeAndAnsi(t *testing.T) {
	requireConPTY(t)
	s := newConptySession("t2", `cmd.exe /c echo done`, "", "cmd", 40, 10, false).(*conptySession)
	if err := s.start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer s.close()
	if err := s.resize(100, 30); err != nil {
		t.Fatalf("resize: %v", err)
	}
	// plain capture must not contain raw ESC bytes.
	if strings.ContainsRune(s.capture(false, false), 0x1b) {
		t.Fatal("plain capture leaked ESC bytes")
	}
}

// TestHostAttachPlumbing verifies the attach pipe: token auth, snapshot
// delivery, and that keystrokes written to the attach pipe reach the session.
func TestHostAttachPlumbing(t *testing.T) {
	pipe, cleanup := startTestHost(t)
	defer cleanup()
	c, err := dialClient(pipe, 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	authClient(t, c)
	if err := c.CreateSession("att", "copilot", "", 80, 24, false); err != nil {
		t.Fatal(err)
	}

	apipe, token, err := c.Attach("att", 80, 24)
	if err != nil {
		t.Fatalf("attach rpc: %v", err)
	}
	if apipe == "" || token == "" {
		t.Fatalf("empty attach pipe/token: %q %q", apipe, token)
	}

	to := 3 * time.Second
	aconn, err := winio.DialPipe(apipe, &to)
	if err != nil {
		t.Fatalf("dial attach pipe: %v", err)
	}
	defer aconn.Close()
	if err := proto.WriteRawFrame(aconn, []byte(token)); err != nil {
		t.Fatalf("write token: %v", err)
	}

	// Snapshot is the first thing written to the pipe.
	_ = aconn.SetReadDeadline(time.Now().Add(3 * time.Second))
	buf := make([]byte, 4096)
	n, err := aconn.Read(buf)
	_ = aconn.SetReadDeadline(time.Time{})
	if err != nil {
		t.Fatalf("read snapshot: %v", err)
	}
	if !strings.Contains(string(buf[:n]), "echo session") {
		t.Fatalf("snapshot missing banner: %q", string(buf[:n]))
	}

	// Keystrokes written to the attach pipe must reach the session.
	if _, err := aconn.Write([]byte("PUMP_OK_42")); err != nil {
		t.Fatalf("write keystrokes: %v", err)
	}
	deadline := time.Now().Add(3 * time.Second)
	var capd string
	for time.Now().Before(deadline) {
		capd, _ = c.CapturePane("att", proto.CaptureScreen, false)
		if strings.Contains(capd, "PUMP_OK_42") {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !strings.Contains(capd, "PUMP_OK_42") {
		t.Fatalf("keystrokes did not reach session: %q", capd)
	}
}

func TestHostAttachRejectsBadToken(t *testing.T) {
	pipe, cleanup := startTestHost(t)
	defer cleanup()
	c, err := dialClient(pipe, 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	authClient(t, c)
	if err := c.CreateSession("att", "copilot", "", 80, 24, false); err != nil {
		t.Fatal(err)
	}
	apipe, _, err := c.Attach("att", 80, 24)
	if err != nil {
		t.Fatal(err)
	}
	to := 3 * time.Second
	aconn, err := winio.DialPipe(apipe, &to)
	if err != nil {
		t.Fatalf("dial attach pipe: %v", err)
	}
	defer aconn.Close()
	if err := proto.WriteRawFrame(aconn, []byte("wrong-token")); err != nil {
		t.Fatalf("write token: %v", err)
	}
	// Host must reject and close: the read should fail rather than deliver a snapshot.
	_ = aconn.SetReadDeadline(time.Now().Add(3 * time.Second))
	if _, err := aconn.Read(make([]byte, 16)); err == nil {
		t.Fatal("expected host to reject a bad attach token")
	}
}
