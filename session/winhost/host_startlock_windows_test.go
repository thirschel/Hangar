//go:build windows

package winhost

import (
	"io"
	"log"
	"testing"
	"time"
)

// TestSlowSessionStartDoesNotBlockGetSession proves the host does not hold h.mu
// across a (possibly slow) session start: while one session is parked mid-start,
// getSession/listSessions — which the ListWorkspaces poll path uses via toInfo —
// must return promptly instead of blocking on the sessions lock. This is the
// regression guard for the 23.6s ListWorkspaces stall. [HOL-1]
func TestSlowSessionStartDoesNotBlockGetSession(t *testing.T) {
	h := newHost(io.Discard, time.Minute)
	entered := make(chan struct{})
	release := make(chan struct{})
	h.newSession = func(name, program, workDir, shell string, cols, rows int, autoYes bool, logger *log.Logger) managedSession {
		f := newFake(name, program, workDir, shell, cols, rows, autoYes, logger).(*fakeSession)
		f.startEntered = entered
		f.startBlock = release
		return f
	}

	startDone := make(chan error, 1)
	go func() { startDone <- h.startManagedSession("slow", "prog", "", 80, 24, false) }()

	// Wait until start() is actually parked (reservation taken, h.mu released).
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("session start never began")
	}

	// While the start is parked, these h.mu.RLock reads must not block.
	reads := make(chan struct{})
	go func() {
		_, _ = h.getSession("anything")
		_ = h.listSessions()
		close(reads)
	}()
	select {
	case <-reads:
	case <-time.After(2 * time.Second):
		t.Fatal("getSession/listSessions blocked while a session start was in progress (h.mu held across start)")
	}

	// The session must not be registered until its start completes.
	if _, ok := h.getSession("slow"); ok {
		t.Fatal("session registered before its start completed")
	}

	// Release the start; it completes and registers the session.
	close(release)
	if err := <-startDone; err != nil {
		t.Fatalf("startManagedSession returned error: %v", err)
	}
	if _, ok := h.getSession("slow"); !ok {
		t.Fatal("session not registered after start completed")
	}
}

// TestFailedSessionStartFreesReservation proves a failed start releases the name
// reservation so a later start can reuse the same name (no permanent
// "already starting"). [HOL-1]
func TestFailedSessionStartFreesReservation(t *testing.T) {
	h := newHost(io.Discard, time.Minute)
	failNext := true
	h.newSession = func(name, program, workDir, shell string, cols, rows int, autoYes bool, logger *log.Logger) managedSession {
		f := newFake(name, program, workDir, shell, cols, rows, autoYes, logger).(*fakeSession)
		f.failStart = failNext
		return f
	}

	if err := h.startManagedSession("ws", "prog", "", 80, 24, false); err == nil {
		t.Fatal("expected the first start to fail")
	}
	h.mu.RLock()
	_, stillStarting := h.starting["ws"]
	h.mu.RUnlock()
	if stillStarting {
		t.Fatal("failed start left a dangling reservation")
	}

	failNext = false
	if err := h.startManagedSession("ws", "prog", "", 80, 24, false); err != nil {
		t.Fatalf("retry after a failed start: %v", err)
	}
	if _, ok := h.getSession("ws"); !ok {
		t.Fatal("session not registered after a successful retry")
	}
}
