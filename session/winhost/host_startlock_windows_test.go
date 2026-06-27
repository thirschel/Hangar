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

// startBlockedFake wires the host factory to hand back a single fake whose start()
// parks until release is closed. The returned getter yields that fake; it is safe
// to call once entered has fired (the factory assigns the fake before start()
// closes entered, establishing a happens-before for the read). [HOL-1]
func startBlockedFake(h *host) (getFake func() *fakeSession, entered, release chan struct{}) {
	entered = make(chan struct{})
	release = make(chan struct{})
	var f *fakeSession
	h.newSession = func(name, program, workDir, shell string, cols, rows int, autoYes bool, logger *log.Logger) managedSession {
		f = newFake(name, program, workDir, shell, cols, rows, autoYes, logger).(*fakeSession)
		f.startEntered = entered
		f.startBlock = release
		return f
	}
	return func() *fakeSession { return f }, entered, release
}

// TestKillDuringStartDoesNotRegister proves killSession, while a session's start
// is parked, cancels the reservation so finishSessionStart closes the
// freshly-started session instead of registering it for an already-killed
// workspace (ArchiveWorkspace teardown calls killSession). [HOL-1]
func TestKillDuringStartDoesNotRegister(t *testing.T) {
	h := newHost(io.Discard, time.Minute)
	getFake, entered, release := startBlockedFake(h)

	startDone := make(chan error, 1)
	go func() { startDone <- h.startManagedSession("ws", "prog", "", 80, 24, false) }()

	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("session start never began")
	}

	// Kill while the start is parked: cancels the in-flight reservation.
	h.killSession("ws")
	close(release)

	if err := <-startDone; err != nil {
		t.Fatalf("a start canceled by kill must not surface an error, got: %v", err)
	}
	if _, ok := h.getSession("ws"); ok {
		t.Fatal("session was registered despite being killed mid-start")
	}
	h.mu.RLock()
	_, stillStarting := h.starting["ws"]
	h.mu.RUnlock()
	if stillStarting {
		t.Fatal("kill-during-start left a dangling reservation")
	}
	// The freshly-started session must have been closed, not leaked.
	if f := getFake(); f == nil || f.alive() {
		t.Fatal("session started after kill was not closed (orphaned child)")
	}
}

// TestShutdownDuringStartDoesNotRegister proves triggerShutdown, while a start is
// in flight, cancels the reservation so the session is closed rather than
// registered (and thus orphaned) past shutdown. [HOL-1]
func TestShutdownDuringStartDoesNotRegister(t *testing.T) {
	h := newHost(io.Discard, time.Minute)
	getFake, entered, release := startBlockedFake(h)

	startDone := make(chan error, 1)
	go func() { startDone <- h.startManagedSession("ws", "prog", "", 80, 24, false) }()

	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("session start never began")
	}

	h.triggerShutdown()
	close(release)

	if err := <-startDone; err != nil {
		t.Fatalf("a start canceled by shutdown must not surface an error, got: %v", err)
	}
	if _, ok := h.getSession("ws"); ok {
		t.Fatal("session was registered despite host shutting down mid-start")
	}
	if f := getFake(); f == nil || f.alive() {
		t.Fatal("session started during shutdown was not closed (orphaned child)")
	}
}

// TestPanicDuringStartFreesReservation proves a panic in start() (recovered
// upstream by safeDispatch) does not wedge the name in h.starting: a later start
// with the same name must succeed. [HOL-1]
func TestPanicDuringStartFreesReservation(t *testing.T) {
	h := newHost(io.Discard, time.Minute)
	panicNext := true
	h.newSession = func(name, program, workDir, shell string, cols, rows int, autoYes bool, logger *log.Logger) managedSession {
		f := newFake(name, program, workDir, shell, cols, rows, autoYes, logger).(*fakeSession)
		f.panicStart = panicNext
		return f
	}

	func() {
		defer func() {
			if r := recover(); r == nil {
				t.Fatal("expected start() to panic")
			}
		}()
		_ = h.startManagedSession("ws", "prog", "", 80, 24, false)
	}()

	h.mu.RLock()
	_, stillStarting := h.starting["ws"]
	h.mu.RUnlock()
	if stillStarting {
		t.Fatal("panic during start left a dangling reservation")
	}

	panicNext = false
	if err := h.startManagedSession("ws", "prog", "", 80, 24, false); err != nil {
		t.Fatalf("retry after a panicked start: %v", err)
	}
	if _, ok := h.getSession("ws"); !ok {
		t.Fatal("session not registered after a successful retry")
	}
}
