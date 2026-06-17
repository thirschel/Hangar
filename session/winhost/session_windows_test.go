//go:build windows

package winhost

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// useSharedClient points the package-level shared client at an in-process test
// host so the Session backend (which normally calls EnsureHost) can be tested.
func useSharedClient(t *testing.T, pipe string) {
	t.Helper()
	c, err := dialClient(pipe, 3*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	authClient(t, c)
	clientMu.Lock()
	sharedClient = c
	clientMu.Unlock()
	t.Cleanup(resetClient)
}

func TestSessionBackendDrivesHost(t *testing.T) {
	requireConPTY(t)
	pipe, cleanup := startRealHost(t)
	defer cleanup()
	useSharedClient(t, pipe)

	s := NewSession("My Title #1", "cmd.exe /c echo HELLO_SESSION_BACKEND")
	if err := s.Start(""); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if !s.DoesSessionExist() {
		t.Fatal("expected session to exist after Start")
	}

	// The program's output must render through the emulator and be captured.
	deadline := time.Now().Add(10 * time.Second)
	var out string
	for time.Now().Before(deadline) {
		out, _ = s.CapturePaneContent()
		if strings.Contains(out, "HELLO_SESSION_BACKEND") {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !strings.Contains(out, "HELLO_SESSION_BACKEND") {
		t.Fatalf("capture never showed program output: %q", out)
	}

	// After the short echo exits, the session is dead -> Restore reports gone,
	// which is what triggers recreate-on-load in instance.Start.
	deadline = time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if errors.Is(s.Restore(), ErrSessionGone) {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if err := s.Restore(); !errors.Is(err, ErrSessionGone) {
		t.Fatalf("expected ErrSessionGone after child exit, got %v", err)
	}

	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if s.DoesSessionExist() {
		t.Fatal("expected session gone after Close")
	}
}

func TestSanitizeSessionName(t *testing.T) {
	cases := map[string]string{
		"My Title":     "cs_My_Title",
		"a.b.c":        "cs_a_b_c",
		"feature/x #2": "cs_feature_x_2",
	}
	for in, want := range cases {
		if got := sanitizeSessionName(in); got != want {
			t.Errorf("sanitizeSessionName(%q)=%q want %q", in, got, want)
		}
	}
}

// TestDetachSafelyKillsSession covers the native-Windows Pause semantics (P7):
// pausing must kill the host session (it can't outlive its removed worktree),
// and pausing an already-gone session must be a no-op.
func TestDetachSafelyKillsSession(t *testing.T) {
	requireConPTY(t)
	pipe, cleanup := startRealHost(t)
	defer cleanup()
	useSharedClient(t, pipe)

	s := NewSession("Pause Me", "cmd.exe /c pause") // `pause` blocks, so the session stays alive
	if err := s.Start(""); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if !s.DoesSessionExist() {
		t.Fatal("expected session to exist after Start")
	}

	if err := s.DetachSafely(); err != nil {
		t.Fatalf("DetachSafely (pause): %v", err)
	}
	if s.DoesSessionExist() {
		t.Fatal("expected session gone after DetachSafely (pause kills it on Windows)")
	}
	// Already gone -> must not error.
	if err := s.DetachSafely(); err != nil {
		t.Fatalf("DetachSafely on a missing session should be nil, got %v", err)
	}
}

// TestSessionSetAutoYes verifies the AutoYes propagation RPC round-trips (P6).
func TestSessionSetAutoYes(t *testing.T) {
	requireConPTY(t)
	pipe, cleanup := startRealHost(t)
	defer cleanup()
	useSharedClient(t, pipe)

	s := NewSession("AutoYes Me", "cmd.exe /c pause")
	if err := s.Start(""); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer s.Close()
	if err := s.SetAutoYes(true); err != nil {
		t.Fatalf("SetAutoYes(true): %v", err)
	}
	if err := s.SetAutoYes(false); err != nil {
		t.Fatalf("SetAutoYes(false): %v", err)
	}
}
