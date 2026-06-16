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
	clientMu.Lock()
	sharedClient = c
	clientMu.Unlock()
	t.Cleanup(resetClient)
}

func TestSessionBackendDrivesHost(t *testing.T) {
	pipe, cleanup := startRealHost(t)
	defer cleanup()
	useSharedClient(t, pipe)

	s := NewSession("My Title #1", "echo HELLO_SESSION_BACKEND")
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
