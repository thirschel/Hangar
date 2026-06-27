//go:build windows

package winhost

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"hangar/session/agentcmd"
	"hangar/session/winhost/proto"
)

func testHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	if err := os.MkdirAll(filepath.Join(home, ".hangar"), 0o700); err != nil {
		t.Fatalf("mkdir config: %v", err)
	}
	return home
}

func injectWorkspace(t *testing.T, h *host, id, program, worktree string) *workspace {
	t.Helper()
	if err := os.MkdirAll(worktree, 0o700); err != nil {
		t.Fatalf("mkdir worktree: %v", err)
	}
	w := &workspace{
		ID: id, Title: "Test", Program: program, RepoPath: t.TempDir(), WorktreePath: worktree,
		Branch: "feature", BaseSHA: "base", SessionName: "ws_" + id, AutoYes: true,
		CreatedUnix: time.Now().Unix(),
	}
	if agentcmd.SupportsResume(program) {
		w.AgentSessionID = newUUID()
	}
	h.workspaces.mu.Lock()
	h.workspaces.wss[id] = w
	h.workspaces.saveLocked()
	h.workspaces.mu.Unlock()
	h.mu.Lock()
	h.sessions[w.SessionName] = newFake(w.SessionName, program, worktree, "cmd", 80, 24, true, nil)
	h.mu.Unlock()
	return w
}

func tinyThresholds(h *host) {
	h.workspaces.thresholds = regenThresholds{stableMs: 20, graceMs: 20, inactivityMs: 80, hardCapMs: 1000}
}

func waitUntil(t *testing.T, timeout time.Duration, pred func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if pred() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition not met before timeout")
}

func waitRegenDone(t *testing.T, h *host, id string) {
	t.Helper()
	waitUntil(t, 5*time.Second, func() bool {
		h.workspaces.mu.Lock()
		defer h.workspaces.mu.Unlock()
		return h.workspaces.regens[id] == nil
	})
}

func TestHandoffReadyDecision(t *testing.T) {
	th := regenThresholds{stableMs: 100, graceMs: 200, inactivityMs: 300, hardCapMs: 400}
	tests := []struct {
		name   string
		s      regenWait
		want   bool
		reason string
	}{
		{"forced", regenWait{forced: true}, true, "forced"},
		{"sentinel", regenWait{sentinelSeen: true, fileChanged: true}, true, "sentinel"},
		{"sentinel-without-file-waits", regenWait{sentinelSeen: true}, false, ""},
		{"file-stable-idle", regenWait{fileChanged: true, fileStableMs: 100, elapsedMs: 200}, true, "file-stable-idle"},
		{"agent-waiting-proceeds", regenWait{fileChanged: true, fileStableMs: 100, elapsedMs: 200, agentWaiting: true}, true, "file-stable-idle"},
		{"busy-waits", regenWait{fileChanged: true, fileStableMs: 100, elapsedMs: 200, agentBusy: true}, false, ""},
		{"not-stable-waits", regenWait{fileChanged: true, fileStableMs: 99, elapsedMs: 200}, false, ""},
		{"within-grace-waits", regenWait{fileChanged: true, fileStableMs: 100, elapsedMs: 199}, false, ""},
		{"inactivity", regenWait{inactiveMs: 300}, true, "inactivity"},
		{"hardcap", regenWait{elapsedMs: 400}, true, "hardcap"},
		{"keep-waiting", regenWait{fileChanged: true, fileStableMs: 10, elapsedMs: 20, inactiveMs: 30}, false, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, reason := handoffReady(tc.s, th)
			if got != tc.want || reason != tc.reason {
				t.Fatalf("handoffReady()=(%v,%q), want (%v,%q)", got, reason, tc.want, tc.reason)
			}
		})
	}
}

func TestRestartAgentRotatesSessionAndID(t *testing.T) {
	home := testHome(t)
	_, h, cleanup := startTestHostWithHandle(t)
	defer cleanup()
	w := injectWorkspace(t, h, "restart1", "copilot", filepath.Join(home, "wt"))
	oldName, oldID := w.SessionName, w.AgentSessionID

	if err := h.workspaces.restartAgent(w.ID, 101, 41); err != nil {
		t.Fatalf("restart: %v", err)
	}
	h.workspaces.mu.Lock()
	got := h.workspaces.wss[w.ID]
	h.workspaces.mu.Unlock()
	if got.SessionName == oldName || !strings.HasPrefix(got.SessionName, "ws_"+w.ID+"-") {
		t.Fatalf("session name did not rotate: old=%q new=%q", oldName, got.SessionName)
	}
	if got.AgentSessionID == oldID || !regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`).MatchString(got.AgentSessionID) {
		t.Fatalf("agent session id not rotated to v4 uuid: old=%q new=%q", oldID, got.AgentSessionID)
	}
	if got.WorktreePath != filepath.Join(home, "wt") || got.Branch != "feature" {
		t.Fatalf("workspace metadata not preserved: %+v", got)
	}
	h.mu.RLock()
	_, oldAlive := h.sessions[oldName]
	_, newAlive := h.sessions[got.SessionName]
	h.mu.RUnlock()
	if oldAlive || !newAlive {
		t.Fatalf("session liveness old=%v new=%v", oldAlive, newAlive)
	}
	data, err := os.ReadFile(filepath.Join(home, ".hangar", "workspaces.json"))
	if err != nil {
		t.Fatalf("read persisted: %v", err)
	}
	if !strings.Contains(string(data), got.SessionName) {
		t.Fatalf("rotated name not persisted: %s", data)
	}

	w2 := injectWorkspace(t, h, "restart2", "claude", filepath.Join(home, "wt2"))
	if err := h.workspaces.restartAgent(w2.ID, 0, 0); err != nil {
		t.Fatalf("restart non-copilot: %v", err)
	}
	if w2.AgentSessionID != "" {
		t.Fatalf("non-copilot AgentSessionID = %q, want empty", w2.AgentSessionID)
	}
}

func TestTranscriptFallbackWritesHandoff(t *testing.T) {
	home := testHome(t)
	_, h, cleanup := startTestHostWithHandle(t)
	defer cleanup()
	w := injectWorkspace(t, h, "fallback1", "copilot", filepath.Join(home, "wt"))
	h.mu.RLock()
	f := h.sessions[w.SessionName].(*fakeSession)
	h.mu.RUnlock()
	_ = f.sendKeys([]byte("known transcript"))
	handoffPath := filepath.Join(w.WorktreePath, "HANDOFF.md")
	h.workspaces.writeTranscriptFallback(w.ID, handoffPath)
	data, err := os.ReadFile(handoffPath)
	if err != nil {
		t.Fatalf("read handoff: %v", err)
	}
	if !strings.Contains(string(data), "# Auto-captured transcript") || !strings.Contains(string(data), "known transcript") {
		t.Fatalf("bad fallback content: %q", data)
	}
}

func TestRegenerateNoHandoffFastPath(t *testing.T) {
	home := testHome(t)
	pipe, h, cleanup := startTestHostWithHandle(t)
	defer cleanup()
	tinyThresholds(h)
	w := injectWorkspace(t, h, "fast1", "copilot", filepath.Join(home, "wt"))
	c, err := dialClient(pipe, 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	authClient(t, c)
	if err := c.RegenerateAgent(w.ID, false, 90, 33); err != nil {
		t.Fatalf("regenerate: %v", err)
	}
	waitRegenDone(t, h, w.ID)
	if _, err := os.Stat(filepath.Join(w.WorktreePath, "HANDOFF.md")); !os.IsNotExist(err) {
		t.Fatalf("HANDOFF.md unexpectedly exists/stat err=%v", err)
	}
	h.workspaces.mu.Lock()
	newName := h.workspaces.wss[w.ID].SessionName
	h.workspaces.mu.Unlock()
	h.mu.RLock()
	_, ok := h.sessions[newName]
	h.mu.RUnlock()
	if !ok {
		t.Fatalf("new session %q not alive", newName)
	}
}

func TestRegenerateHandoffWritesAndSeeds(t *testing.T) {
	home := testHome(t)
	pipe, h, cleanup := startTestHostWithHandle(t)
	defer cleanup()
	tinyThresholds(h)
	w := injectWorkspace(t, h, "handoff1", "copilot", filepath.Join(home, "wt"))
	h.mu.RLock()
	old := h.sessions[w.SessionName].(*fakeSession)
	h.mu.RUnlock()
	old.sendHook = func(f *fakeSession, b []byte) error {
		if strings.Contains(string(b), "write a") {
			return os.WriteFile(filepath.Join(f.workDir, "HANDOFF.md"), []byte("usable handoff content for the next agent"), 0o600)
		}
		return nil
	}
	c, err := dialClient(pipe, 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	authClient(t, c)
	if err := c.RegenerateAgent(w.ID, true, 90, 33); err != nil {
		t.Fatalf("regenerate: %v", err)
	}
	waitRegenDone(t, h, w.ID)
	h.workspaces.mu.Lock()
	newName := h.workspaces.wss[w.ID].SessionName
	h.workspaces.mu.Unlock()
	h.mu.RLock()
	newFake := h.sessions[newName].(*fakeSession)
	h.mu.RUnlock()
	if got := newFake.capture(true, false); !strings.Contains(got, "Read it first") {
		t.Fatalf("new session was not seeded: %q", got)
	}
}

func TestForceRegenerateShortCircuits(t *testing.T) {
	home := testHome(t)
	pipe, h, cleanup := startTestHostWithHandle(t)
	defer cleanup()
	h.workspaces.thresholds = regenThresholds{stableMs: 10000, graceMs: 10000, inactivityMs: 10000, hardCapMs: 30000}
	w := injectWorkspace(t, h, "force1", "copilot", filepath.Join(home, "wt"))
	c, err := dialClient(pipe, 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	authClient(t, c)
	if err := c.RegenerateAgent(w.ID, true, 80, 24); err != nil {
		t.Fatalf("regenerate: %v", err)
	}
	waitUntil(t, 2*time.Second, func() bool {
		h.workspaces.mu.Lock()
		defer h.workspaces.mu.Unlock()
		return h.workspaces.regens[w.ID] != nil && h.workspaces.regens[w.ID].phase == "handoff"
	})
	if err := c.ForceRegenerate(w.ID); err != nil {
		t.Fatalf("force: %v", err)
	}
	waitRegenDone(t, h, w.ID)
	data, err := os.ReadFile(filepath.Join(w.WorktreePath, "HANDOFF.md"))
	if err != nil || !strings.Contains(string(data), "# Auto-captured transcript") {
		t.Fatalf("fallback not written after force: %q err=%v", data, err)
	}
}

func TestRegenerateInactivityFallback(t *testing.T) {
	home := testHome(t)
	pipe, h, cleanup := startTestHostWithHandle(t)
	defer cleanup()
	tinyThresholds(h)
	w := injectWorkspace(t, h, "inactive1", "copilot", filepath.Join(home, "wt"))
	c, err := dialClient(pipe, 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	authClient(t, c)
	if err := c.RegenerateAgent(w.ID, true, 80, 24); err != nil {
		t.Fatalf("regenerate: %v", err)
	}
	waitRegenDone(t, h, w.ID)
	data, err := os.ReadFile(filepath.Join(w.WorktreePath, "HANDOFF.md"))
	if err != nil || !strings.Contains(string(data), "# Auto-captured transcript") {
		t.Fatalf("fallback not written after inactivity: %q err=%v", data, err)
	}
	h.workspaces.mu.Lock()
	newName := h.workspaces.wss[w.ID].SessionName
	h.workspaces.mu.Unlock()
	h.mu.RLock()
	_, ok := h.sessions[newName]
	h.mu.RUnlock()
	if !ok {
		t.Fatalf("new session %q not alive", newName)
	}
}

func TestArchiveDuringRegenerateNoZombie(t *testing.T) {
	home := testHome(t)
	pipe, h, cleanup := startTestHostWithHandle(t)
	defer cleanup()
	h.workspaces.thresholds = regenThresholds{stableMs: 10000, graceMs: 10000, inactivityMs: 10000, hardCapMs: 30000}
	w := injectWorkspace(t, h, "archive1", "copilot", filepath.Join(home, ".hangar", "worktrees", "wt"))
	c, err := dialClient(pipe, 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	authClient(t, c)
	if err := c.RegenerateAgent(w.ID, true, 80, 24); err != nil {
		t.Fatalf("regenerate: %v", err)
	}
	if err := c.ArchiveWorkspace(w.ID, true); err != nil {
		t.Fatalf("archive: %v", err)
	}
	// The workspace is removed from the registry synchronously, so ListWorkspaces
	// reflects the archive on the very next poll even though teardown is deferred. [#3]
	h.workspaces.mu.Lock()
	_, stillWorkspace := h.workspaces.wss[w.ID]
	h.workspaces.mu.Unlock()
	if stillWorkspace {
		t.Fatalf("workspace still present in registry immediately after archive")
	}
	// The regen wind-down + session kill now run in the archive teardown goroutine
	// (archive returns immediately instead of blocking the serial control pipe on
	// regen.done for up to 60s), so poll for the regeneration to clear and the
	// session to disappear rather than expecting them synchronously. [#3]
	waitRegenDone(t, h, w.ID)
	zombie := ""
	for i := 0; i < 60; i++ {
		zombie = ""
		h.mu.RLock()
		for name := range h.sessions {
			if strings.Contains(name, w.ID) {
				zombie = name
				break
			}
		}
		h.mu.RUnlock()
		if zombie == "" {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if zombie != "" {
		t.Fatalf("zombie session remains after archive (waited 3s): %s", zombie)
	}
}

// TestArchiveReturnsWhileRegenActive proves #3: archive must NOT block the serial
// control pipe waiting on an in-flight regeneration. It injects a regenState whose
// done channel is never closed (no goroutine), then asserts archive still returns
// promptly and removes the workspace from the registry synchronously, with the
// regen wind-down deferred to the background teardown goroutine.
func TestArchiveReturnsWhileRegenActive(t *testing.T) {
	home := testHome(t)
	_, h, cleanup := startTestHostWithHandle(t)
	defer cleanup()
	m := h.workspaces
	w := injectWorkspace(t, h, "archfast1", "copilot", filepath.Join(home, ".hangar", "worktrees", "wt"))

	// A regeneration that never completes: nothing ever closes done. Pre-#3 archive
	// waited inline on this channel (up to 60s), stalling every other RPC on the
	// connection; post-#3 it tombstones, removes from the registry, and returns,
	// moving the done-wait into the teardown goroutine (which unblocks at cleanup
	// via shutdownCh).
	regen := &regenState{force: make(chan struct{}), done: make(chan struct{})}
	m.mu.Lock()
	m.regens[w.ID] = regen
	m.mu.Unlock()

	start := time.Now()
	resp := m.archive(&proto.Request{ID: 1, WorkspaceID: w.ID})
	elapsed := time.Since(start)
	if resp == nil || !resp.OK {
		t.Fatalf("archive failed: %+v", resp)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("archive blocked %v on regen.done; it must return immediately and defer the wait", elapsed)
	}

	// Removed from the registry synchronously (so ListWorkspaces reflects it now),
	// tombstoned (so a concurrent archive is a no-op), and the regen was signalled
	// to cancel.
	m.mu.Lock()
	_, stillWorkspace := m.wss[w.ID]
	tombstoned := m.tombstone[w.ID]
	m.mu.Unlock()
	if stillWorkspace {
		t.Fatalf("workspace not removed from registry synchronously")
	}
	if !tombstoned {
		t.Fatalf("tombstone not set while a regeneration is in flight")
	}
	select {
	case <-regen.force:
	default:
		t.Fatalf("archive did not signal the in-flight regeneration to cancel")
	}
}

// TestRestartAgentRichRoutesToSDK proves #2: regenerating a rich (Copilot SDK)
// workspace must restart it through the SDK backend, not the ConPTY terminal
// factory. Pre-#2 restartAgent always used startManagedSessionWithShell, so a rich
// workspace's live session silently became a terminal one (Kind stayed rich) and
// the desktop's OpenRichStream then failed its *sdkSession assertion. Regenerate
// is a FRESH conversation, so the SDK start must use resume=false while carrying
// the workspace's persisted model selection.
func TestRestartAgentRichRoutesToSDK(t *testing.T) {
	home := testHome(t)
	_, h, cleanup := startTestHostWithHandle(t)
	defer cleanup()
	m := h.workspaces
	w := injectWorkspace(t, h, "richregen1", "copilot", filepath.Join(home, "wt"))
	// Mark it rich (the create/resume path sets Kind; injectWorkspace defaults to
	// terminal) and give it a persisted model selection to carry across the restart.
	m.mu.Lock()
	w.Kind = proto.WorkspaceKindRich
	w.Model = "gpt-5"
	m.saveLocked()
	m.mu.Unlock()
	oldName := w.SessionName

	// The terminal factory must never be used for a rich regenerate.
	h.newSession = func(name, program, workDir, shell string, cols, rows int, autoYes bool, logger *log.Logger) managedSession {
		t.Errorf("rich regenerate used the ConPTY terminal factory (newSession) for %q", name)
		return newFake(name, program, workDir, shell, cols, rows, autoYes, logger)
	}

	var calls int
	var gotResume bool
	var gotName, gotModel string
	h.startSDKSessionHook = func(p sdkSessionParams) error {
		calls++
		gotResume, gotName, gotModel = p.resume, p.name, p.model
		h.mu.Lock()
		h.sessions[p.name] = &fakeSession{name: p.name, program: p.program, workDir: p.workDir, autoYes: p.autoYes, aliveFlag: true}
		h.mu.Unlock()
		return nil
	}

	if err := m.restartAgent(w.ID, 80, 24); err != nil {
		t.Fatalf("restart: %v", err)
	}
	if calls != 1 {
		t.Fatalf("expected exactly one SDK start, got %d", calls)
	}
	if gotResume {
		t.Fatalf("rich regenerate must start a FRESH conversation (resume=false), got resume=true")
	}
	if gotModel != "gpt-5" {
		t.Fatalf("rich regenerate dropped the persisted model: got %q, want %q", gotModel, "gpt-5")
	}

	m.mu.Lock()
	newName := m.wss[w.ID].SessionName
	m.mu.Unlock()
	if newName == oldName {
		t.Fatalf("session name did not rotate: %q", newName)
	}
	if gotName != newName {
		t.Fatalf("SDK session registered under %q, but workspace points at %q", gotName, newName)
	}
	h.mu.RLock()
	_, oldAlive := h.sessions[oldName]
	_, newAlive := h.sessions[newName]
	h.mu.RUnlock()
	if oldAlive {
		t.Fatalf("old session %q not killed", oldName)
	}
	if !newAlive {
		t.Fatalf("new SDK session %q not registered", newName)
	}
}

func TestRegenerateStartFailureRevivable(t *testing.T) {
	home := testHome(t)
	pipe, h, cleanup := startTestHostWithHandle(t)
	defer cleanup()
	w := injectWorkspace(t, h, "fail1", "copilot", filepath.Join(home, "wt"))
	oldName := w.SessionName
	h.newSession = func(name, program, workDir, shell string, cols, rows int, autoYes bool, logger *log.Logger) managedSession {
		f := newFake(name, program, workDir, shell, cols, rows, autoYes, logger).(*fakeSession)
		f.failStart = true
		return f
	}
	c, err := dialClient(pipe, 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	authClient(t, c)
	if err := c.RegenerateAgent(w.ID, false, 80, 24); err != nil {
		t.Fatalf("regenerate: %v", err)
	}
	waitRegenDone(t, h, w.ID)
	h.workspaces.mu.Lock()
	rotated := h.workspaces.wss[w.ID].SessionName
	h.workspaces.mu.Unlock()
	if rotated == oldName {
		t.Fatalf("session name not rotated on failed start")
	}
	var persisted []*workspace
	data, err := os.ReadFile(filepath.Join(home, ".hangar", "workspaces.json"))
	if err != nil {
		t.Fatalf("read persisted: %v", err)
	}
	if err := json.Unmarshal(data, &persisted); err != nil {
		t.Fatalf("unmarshal persisted: %v", err)
	}
	if len(persisted) == 0 || persisted[0].SessionName != rotated {
		t.Fatalf("rotated name not persisted: got %q want %q", string(data), rotated)
	}
	h.mu.RLock()
	_, oldAlive := h.sessions[oldName]
	_, newAlive := h.sessions[rotated]
	h.mu.RUnlock()
	if oldAlive || newAlive {
		t.Fatalf("unexpected live sessions after failed restart old=%v new=%v", oldAlive, newAlive)
	}
}

var _ managedSession = (*fakeSession)(nil)
