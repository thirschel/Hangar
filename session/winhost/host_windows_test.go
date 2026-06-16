//go:build windows

package winhost

import (
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"claude-squad/session/winhost/proto"

	"github.com/Microsoft/go-winio"
)

// startTestHost starts an in-process host on a unique pipe (no detached process,
// no lock file) so the protocol + lifecycle can be tested over real go-winio
// transport.
func startTestHost(t *testing.T) (string, func()) {
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
	go h.serve(ln)
	return pipe, func() { h.triggerShutdown() }
}

func TestHostLifecycle(t *testing.T) {
	pipe, cleanup := startTestHost(t)
	defer cleanup()

	c, err := dialClient(pipe, 3*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer c.Close()

	r, err := c.Hello()
	if err != nil || !r.OK || r.HostVersion != proto.Version {
		t.Fatalf("hello: resp=%+v err=%v", r, err)
	}

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
	if err := c.Shutdown(); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	c.Close()

	// After shutdown the listener is closed; new dials must fail.
	time.Sleep(300 * time.Millisecond)
	if c2, err := dialClient(pipe, 500*time.Millisecond); err == nil {
		c2.Close()
		t.Fatal("expected dial to fail after shutdown")
	}
}

func TestConcurrentClients(t *testing.T) {
	pipe, cleanup := startTestHost(t)
	defer cleanup()

	// Two independent control connections operate concurrently.
	c1, err := dialClient(pipe, 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer c1.Close()
	c2, err := dialClient(pipe, 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer c2.Close()

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
