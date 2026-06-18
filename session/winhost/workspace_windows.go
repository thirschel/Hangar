//go:build windows

package winhost

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"hangar/config"
	"hangar/session/agentcmd"
	"hangar/session/copilot"
	"hangar/session/git"
	"hangar/session/winhost/proto"
)

// workspace is a unit of parallel agent work: a git worktree + branch plus an
// agent terminal session (a normal host session named SessionName). The host
// owns these directly (it creates the ConPTY + worktree itself, not via the
// RPC client), so the desktop app is a thin client over this manager.
type workspace struct {
	ID             string `json:"id"`
	Title          string `json:"title"`
	Program        string `json:"program"`
	RepoPath       string `json:"repoPath"`
	WorktreePath   string `json:"worktreePath"`
	Branch         string `json:"branch"`
	BaseSHA        string `json:"baseSHA"`
	SessionName    string `json:"sessionName"`
	AutoYes        bool   `json:"autoYes"`
	ExistingBranch bool   `json:"existingBranch"`
	CreatedUnix    int64  `json:"createdUnix"`
	RunCommand     string `json:"runCommand"`
	AgentSessionID string `json:"agentSessionId"`  // stable agent session UUID for resume (copilot)
	Shell          string `json:"shell,omitempty"` // "cmd", "powershell", "pwsh"; empty = config default
}

type workspaceManager struct {
	mu          sync.Mutex
	host        *host
	wss         map[string]*workspace // by ID
	regens      map[string]*regenState
	tombstone   map[string]bool
	thresholds  regenThresholds
	bootFloor   time.Duration // min wait for a (re)started agent to be input-ready
	submitDelay time.Duration // settle between typing a prompt and the submit Enter

	// diffMu guards diffCache. Diff stats are computed off the request path (by a
	// background refresher) so ListWorkspaces never runs git: git add -N . walks
	// the whole worktree and a pathological tree (e.g. a symlink into the Windows
	// assembly cache) can make it take minutes, which previously blocked the
	// single control connection and starved every other RPC (the session browser).
	diffMu    sync.Mutex
	diffCache map[string]cachedDiff
}

// cachedDiff is a last-known added/removed line count for a workspace.
type cachedDiff struct {
	added   int
	removed int
}

const (
	// diffRefreshInterval is how often the background refresher recomputes each
	// workspace's diff stats.
	diffRefreshInterval = 4 * time.Second
	// diffComputeTimeout bounds a single workspace's git diff so one bad worktree
	// cannot stall the refresher (or a cold-start read) for more than this long.
	diffComputeTimeout = 8 * time.Second
)

func newWorkspaceManager(h *host) *workspaceManager {
	m := &workspaceManager{
		host:      h,
		wss:       map[string]*workspace{},
		regens:    map[string]*regenState{},
		tombstone: map[string]bool{},
		diffCache: map[string]cachedDiff{},
		thresholds: regenThresholds{
			stableMs: 1500, graceMs: 4000, inactivityMs: 30000, hardCapMs: 300000,
		},
		bootFloor:   bootReadyFloor,
		submitDelay: promptSubmitDelay,
	}
	m.load()
	return m
}

// --- persistence (workspaces.json next to the rest of ~/.hangar state) ---

func workspacesPath() (string, error) {
	dir, err := config.GetConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "workspaces.json"), nil
}

func (m *workspaceManager) load() {
	p, err := workspacesPath()
	if err != nil {
		return
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return
	}
	var list []*workspace
	if json.Unmarshal(data, &list) != nil {
		return
	}
	for _, w := range list {
		// workspaces.json is untrusted state on disk. Validate the fields that
		// feed dangerous git/filesystem operations before accepting the entry:
		// a poisoned baseSHA enables `git diff --output=` injection (F-08) and a
		// poisoned worktreePath enables arbitrary deletion (F-09). Skip — rather
		// than load — any entry that fails.
		if w.BaseSHA != "" {
			if err := git.ValidateSHA(w.BaseSHA); err != nil {
				m.host.logger.Printf("workspaces.json: skipping workspace %q: %v", w.ID, err)
				continue
			}
		}
		if w.WorktreePath != "" {
			if err := git.AssertWorktreePathContained(w.WorktreePath); err != nil {
				m.host.logger.Printf("workspaces.json: skipping workspace %q: unsafe worktree path: %v", w.ID, err)
				continue
			}
		}
		m.wss[w.ID] = w
	}
}

// saveLocked persists the registry. Caller must hold m.mu.
func (m *workspaceManager) saveLocked() {
	p, err := workspacesPath()
	if err != nil {
		return
	}
	list := make([]*workspace, 0, len(m.wss))
	for _, w := range m.wss {
		list = append(list, w)
	}
	if data, err := json.MarshalIndent(list, "", "  "); err == nil {
		_ = os.WriteFile(p, data, 0o600)
	}
}

// --- helpers ---

var wsSlugRe = regexp.MustCompile(`[^a-zA-Z0-9]+`)

func slug(s string) string {
	s = strings.Trim(wsSlugRe.ReplaceAllString(s, "-"), "-")
	if s == "" {
		s = "workspace"
	}
	return strings.ToLower(s)
}

func newWorkspaceID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

func shortRand() string {
	var b [3]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// bootReadyFloor is the minimum time to wait for a freshly (re)started agent
// before sending it input. A just-spawned CLI flushes stdin during boot, so input
// sent too early is dropped; this floor (plus a saw-output + settle check) keeps
// the handoff/seed prompts from landing on a not-yet-ready agent.
const bootReadyFloor = 3 * time.Second

// promptSubmitDelay is the settle time between typing a prompt's text and sending
// the submit Enter as a separate keystroke (and between Enter retries). It must be
// long enough that the agent processes the text paste and the CR in distinct reads
// so the CR submits rather than being absorbed into the input.
const promptSubmitDelay = 350 * time.Millisecond

const trustApprovalTTL = 15 * time.Second

const (
	// handoffPrompt and seedPrompt are SINGLE-LINE on purpose: a multi-line prompt
	// (embedded \n) is typed into the agent's editor as newlines and the trailing
	// Enter then fails to submit the buffer, so the prompt is never sent. One line
	// + one Enter is the submit contract the desktop composer also uses.
	handoffPrompt = "You are about to be replaced by a fresh agent in this same workspace. Before that, write a " +
		"handoff document named HANDOFF.md in the current working directory. Capture, concisely: " +
		"Task — what you were asked to do and the goal; " +
		"Current status — what is done, in progress, and what works or does not; " +
		"Next steps — the concrete actions the next agent should take, in order; " +
		"Key context — important files, decisions, constraints, gotchas, and build/test commands. " +
		"Write ONLY that file; make no other changes. When it is fully written, print: HANDOFF_COMPLETE"
	seedPrompt = "A previous agent in this workspace left a handoff at HANDOFF.md in the current working directory. " +
		"Read it first, then continue the work it describes. If anything is unclear, inspect the relevant files before proceeding."
	handoffSentinel = "HANDOFF_COMPLETE"
)

var errArchived = errors.New("workspace archived")

type regenState struct {
	phase     string
	force     chan struct{}
	done      chan struct{}
	closeOnce sync.Once
}

type regenThresholds struct{ stableMs, graceMs, inactivityMs, hardCapMs int64 }

type regenWait struct {
	sentinelSeen bool
	fileChanged  bool
	fileStableMs int64
	agentBusy    bool
	agentWaiting bool
	inactiveMs   int64
	elapsedMs    int64
	forced       bool
}

func handoffReady(s regenWait, th regenThresholds) (proceed bool, reason string) {
	switch {
	case s.forced:
		return true, "forced"
	case s.sentinelSeen && s.fileChanged:
		return true, "sentinel"
	case s.fileChanged && s.fileStableMs >= th.stableMs && !s.agentBusy && s.elapsedMs >= th.graceMs:
		return true, "file-stable-idle"
	case s.inactiveMs >= th.inactivityMs:
		return true, "inactivity"
	case s.elapsedMs >= th.hardCapMs:
		return true, "hardcap"
	default:
		return false, ""
	}
}

// newUUID returns a random RFC-4122 v4 UUID, used as a stable agent session id.
func newUUID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant 10
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// defaultTitle is the placeholder name for a workspace created without a title:
// the repo folder name, shown until the agent renames it after the first message.
func defaultTitle(repoPath string) string {
	base := filepath.Base(strings.TrimRight(repoPath, `\/`))
	if base == "" || base == "." || base == string(filepath.Separator) {
		return "workspace"
	}
	return base
}

// titleAnsiRe strips ANSI/VT escape sequences from one-shot agent output before
// it is used as a title.
var titleAnsiRe = regexp.MustCompile("\x1b\\[[0-9;?]*[ -/]*[@-~]")

// supportsTitleGen reports whether we know a one-shot, non-interactive mode for
// this agent that can summarize a task into a title. Currently only copilot (its
// `-p` flag prints a reply to stdout and exits).
func supportsTitleGen(program string) bool {
	return strings.Contains(strings.ToLower(program), "copilot")
}

// generateTitle asks the workspace's agent to name the task from the user's first
// message and persists the result as the workspace title. The agent call can take
// a few seconds, so it runs in the background; the app picks up the new title on
// its next poll. Unsupported agents and any failure fall back to a title derived
// from the message, so a title is always set.
func (m *workspaceManager) generateTitle(req *proto.Request) *proto.Response {
	message := strings.TrimSpace(req.Message)
	if message == "" {
		return proto.Errorf(req.ID, "message required")
	}
	m.mu.Lock()
	w, ok := m.wss[req.WorkspaceID]
	if !ok {
		m.mu.Unlock()
		return proto.Errorf(req.ID, "no such workspace: %s", req.WorkspaceID)
	}
	id, program := w.ID, w.Program
	m.mu.Unlock()

	go func() {
		title := m.titleFor(program, message)
		if title == "" {
			return
		}
		m.mu.Lock()
		if w, ok := m.wss[id]; ok {
			w.Title = title
			m.saveLocked()
		}
		m.mu.Unlock()
		m.host.logger.Printf("titled workspace %s -> %q", id, title)
	}()

	return &proto.Response{ID: req.ID, OK: true}
}

// titleFor produces a short workspace title from the user's first message. It
// uses a one-shot agent call when we know how (copilot); otherwise — or on any
// failure/empty/timeout — it falls back to a title derived from the message.
func (m *workspaceManager) titleFor(program, message string) string {
	if supportsTitleGen(program) {
		if t := m.agentTitle(program, message); t != "" {
			return t
		}
	}
	return deriveTitle(message)
}

// agentTitle runs a one-shot, non-interactive agent invocation to summarize the
// first message into a short title (copilot's `-p` prints to stdout and exits).
// It runs windowless in a neutral temp cwd (so the agent can't touch the
// worktree), bounded by a timeout, and the output is sanitized to one short line.
// Returns "" on any failure so the caller can fall back.
func (m *workspaceManager) agentTitle(program, message string) string {
	fields := strings.Fields(program)
	if len(fields) == 0 {
		return ""
	}
	prompt := "Reply with ONLY a short 3 to 6 word title for the following coding task. " +
		"No quotes, no trailing punctuation, no explanation.\n\nTask: " + message

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, fields[0], "-p", prompt)
	cmd.Dir = os.TempDir()
	hideConsole(cmd)
	out, err := cmd.Output()
	if err != nil {
		m.host.logger.Printf("title generation via %q failed: %v", fields[0], err)
		return ""
	}
	return sanitizeTitle(string(out))
}

// deriveTitle makes a short title from the first non-empty line of the user's
// message — the fallback when the agent can't generate one.
func deriveTitle(message string) string {
	for _, line := range strings.Split(message, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			return truncateTitle(line)
		}
	}
	return "workspace"
}

// sanitizeTitle reduces raw agent output to a single short, clean title line.
func sanitizeTitle(s string) string {
	s = titleAnsiRe.ReplaceAllString(s, "")
	for _, line := range strings.Split(s, "\n") {
		line = strings.Map(func(r rune) rune {
			if r < 0x20 || r == 0x7f {
				return -1
			}
			return r
		}, line)
		line = strings.TrimSpace(strings.Trim(strings.TrimSpace(line), "\"'`*.#"))
		if line != "" {
			return truncateTitle(line)
		}
	}
	return ""
}

// truncateTitle caps a title to a sane length (a few words).
func truncateTitle(s string) string {
	const maxChars = 60
	if words := strings.Fields(s); len(words) > 8 {
		s = strings.Join(words[:8], " ")
	}
	if len(s) > maxChars {
		s = strings.TrimSpace(s[:maxChars])
	}
	return s
}

// worktreeFor reconstructs a GitWorktree handle from stored metadata so we can
// run diff/remove without re-resolving paths.
func (w *workspace) worktreeFor() *git.GitWorktree {
	return git.NewGitWorktreeFromStorage(w.RepoPath, w.WorktreePath, w.SessionName, w.Branch, w.BaseSHA, w.ExistingBranch)
}

func (m *workspaceManager) toInfo(w *workspace) proto.WorkspaceInfo {
	alive := false
	busy, waiting := false, false
	if s, ok := m.host.getSession(w.SessionName); ok {
		alive = s.alive()
		busy, waiting = s.agentStatus()
	}
	regenerating, phase := false, ""
	if regen, ok := m.regens[w.ID]; ok {
		regenerating, phase = true, regen.phase
	}
	running, previewURL := m.host.runs.info(w.ID)
	// Diff stats are served from the cache populated by the background refresher,
	// so building a workspace info never runs git on the request path.
	added, removed := m.cachedDiffFor(w.ID)
	return proto.WorkspaceInfo{
		ID: w.ID, Title: w.Title, Program: w.Program, RepoPath: w.RepoPath,
		WorktreePath: w.WorktreePath, Branch: w.Branch, SessionName: w.SessionName,
		Alive: alive, AutoYes: w.AutoYes, Added: added, Removed: removed, CreatedUnix: w.CreatedUnix,
		RunCommand: w.RunCommand, Running: running, PreviewURL: previewURL,
		Busy: busy, Waiting: waiting,
		Regenerating: regenerating, RegenPhase: phase, Shell: w.Shell,
	}
}

// --- diff stat cache (kept warm off the request path) ---

// cachedDiffFor returns the last-known added/removed counts for a workspace, or
// (0, 0) before the background refresher has computed them. Callers must not run
// git here: this is read on the hot ListWorkspaces path.
func (m *workspaceManager) cachedDiffFor(id string) (added, removed int) {
	m.diffMu.Lock()
	defer m.diffMu.Unlock()
	d := m.diffCache[id]
	return d.added, d.removed
}

// startDiffRefresh launches the background goroutine that keeps diffCache warm.
// It is started only by the production daemon (RunHost), never in unit tests, so
// tests do not race the test's own git operations on the same worktree.
func (m *workspaceManager) startDiffRefresh() {
	go m.diffRefreshLoop()
}

// diffRefreshLoop periodically recomputes every workspace's diff stats off the
// request path (bounded per workspace by diffComputeTimeout) and prunes entries
// for archived workspaces, so ListWorkspaces is always a fast cache read.
func (m *workspaceManager) diffRefreshLoop() {
	defer recoverGoroutine("workspace.diffRefreshLoop")
	ticker := time.NewTicker(diffRefreshInterval)
	defer ticker.Stop()
	for {
		m.refreshAllDiffs()
		select {
		case <-m.host.shutdownCh:
			return
		case <-ticker.C:
		}
	}
}

// refreshAllDiffs recomputes diff stats for every current workspace without
// holding m.mu during the git work, then stores the results and drops stale keys.
func (m *workspaceManager) refreshAllDiffs() {
	m.mu.Lock()
	type job struct {
		id string
		wt *git.GitWorktree
	}
	jobs := make([]job, 0, len(m.wss))
	live := make(map[string]struct{}, len(m.wss))
	for id, w := range m.wss {
		jobs = append(jobs, job{id: id, wt: w.worktreeFor()})
		live[id] = struct{}{}
	}
	m.mu.Unlock()

	for _, j := range jobs {
		select {
		case <-m.host.shutdownCh:
			return
		default:
		}
		stats := j.wt.DiffNumstatTimeout(diffComputeTimeout)
		if stats == nil || stats.Error != nil {
			// Keep the previous value on error/timeout rather than flapping to 0.
			continue
		}
		m.diffMu.Lock()
		m.diffCache[j.id] = cachedDiff{added: stats.Added, removed: stats.Removed}
		m.diffMu.Unlock()
	}

	// Drop cache entries for workspaces that no longer exist.
	m.diffMu.Lock()
	for id := range m.diffCache {
		if _, ok := live[id]; !ok {
			delete(m.diffCache, id)
		}
	}
	m.diffMu.Unlock()
}

// --- RPC handlers ---

func (m *workspaceManager) list(req *proto.Request) *proto.Response {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]proto.WorkspaceInfo, 0, len(m.wss))
	for _, w := range m.wss {
		out = append(out, m.toInfo(w))
	}
	// Map iteration order is random; sort by creation time (then ID) so the list
	// is stable across polls and the sidebar doesn't reshuffle.
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedUnix != out[j].CreatedUnix {
			return out[i].CreatedUnix < out[j].CreatedUnix
		}
		return out[i].ID < out[j].ID
	})
	return &proto.Response{ID: req.ID, OK: true, Workspaces: out}
}

func (m *workspaceManager) get(req *proto.Request) *proto.Response {
	m.mu.Lock()
	defer m.mu.Unlock()
	w, ok := m.wss[req.WorkspaceID]
	if !ok {
		return proto.Errorf(req.ID, "no such workspace: %s", req.WorkspaceID)
	}
	info := m.toInfo(w)
	return &proto.Response{ID: req.ID, OK: true, Workspace: &info}
}

// reviveBySession recreates the agent session for the workspace whose SessionName
// matches, when that session is currently missing or dead. After a daemon restart
// (or reboot) we only load workspace metadata from disk, not the live sessions, so
// the first attach must resurrect the session from the persisted program/worktree
// (this is exactly the Pause -> Resume semantics). Returns true if a matching
// workspace was found and its session is now alive; false (with no error) if no
// workspace owns this session name.
func (m *workspaceManager) reviveBySession(sessionName string, cols, rows int) (bool, error) {
	m.mu.Lock()
	var w *workspace
	for _, cand := range m.wss {
		if cand.SessionName == sessionName {
			w = cand
			break
		}
	}
	m.mu.Unlock()
	if w == nil {
		return false, nil
	}
	if s, ok := m.host.getSession(sessionName); ok {
		if s.alive() {
			return true, nil
		}
		// A dead session object lingers under this name; remove it so the name is
		// free for startManagedSession to recreate.
		m.host.killSession(sessionName)
	}
	cols, rows = sizeOr(cols, 120), sizeOr(rows, 30)
	shell := w.Shell
	if shell == "" {
		shell = "cmd"
	}
	// Re-validate the persisted AgentSessionID before reusing it on revive. A
	// tampered workspaces.json could otherwise smuggle a poisoned id back into
	// the launch path (F-01); if it no longer passes the trust-boundary gate we
	// launch fresh (no resume) rather than trusting stored state.
	program := w.Program
	if w.AgentSessionID != "" {
		if agentcmd.ValidSessionID(w.AgentSessionID) {
			program = agentcmd.ResumeCommand(w.Program, w.AgentSessionID)
		} else {
			m.host.logger.Printf("workspace %s: rejecting invalid persisted AgentSessionID %q; launching without resume", w.ID, w.AgentSessionID)
		}
	}
	if err := m.host.startManagedSessionWithShell(w.SessionName, program, w.WorktreePath, shell, cols, rows, w.AutoYes); err != nil {
		return false, err
	}
	return true, nil
}

func (m *workspaceManager) regenerate(req *proto.Request) *proto.Response {
	m.mu.Lock()
	w, ok := m.wss[req.WorkspaceID]
	if !ok {
		m.mu.Unlock()
		return proto.Errorf(req.ID, "no such workspace: %s", req.WorkspaceID)
	}
	if _, exists := m.regens[w.ID]; exists {
		m.mu.Unlock()
		return proto.Errorf(req.ID, "workspace is already regenerating: %s", req.WorkspaceID)
	}
	phase := "restarting"
	if req.Handoff {
		phase = "handoff"
	}
	m.regens[w.ID] = &regenState{phase: phase, force: make(chan struct{}), done: make(chan struct{})}
	id := w.ID
	cols, rows := sizeOr(req.Cols, 120), sizeOr(req.Rows, 30)
	handoff := req.Handoff
	m.mu.Unlock()

	m.host.logger.Printf("regenerate requested ws=%s handoff=%v cols=%d rows=%d", id, handoff, cols, rows)
	go m.runRegenerate(id, handoff, cols, rows)
	return &proto.Response{ID: req.ID, OK: true}
}

func (m *workspaceManager) forceRegenerate(req *proto.Request) *proto.Response {
	m.mu.Lock()
	regen := m.regens[req.WorkspaceID]
	m.mu.Unlock()
	if regen != nil {
		regen.closeOnce.Do(func() { close(regen.force) })
	}
	return &proto.Response{ID: req.ID, OK: true}
}

func (m *workspaceManager) runRegenerate(id string, handoff bool, cols, rows int) {
	m.mu.Lock()
	regen := m.regens[id]
	m.mu.Unlock()
	if regen == nil {
		return
	}
	defer recoverGoroutine("workspace.regenerate")
	defer close(regen.done)
	defer func() {
		m.mu.Lock()
		delete(m.regens, id)
		m.mu.Unlock()
	}()
	defer m.clearWorkspaceTrustApproval(id)

	cancelled := func() bool {
		select {
		case <-regen.force:
			return true
		default:
		}
		m.mu.Lock()
		tombstoned := m.tombstone[id]
		m.mu.Unlock()
		if tombstoned {
			return true
		}
		select {
		case <-m.host.shutdownCh:
			return true
		default:
			return false
		}
	}

	if handoff {
		m.runHandoff(id, regen, cancelled)
	}

	m.setRegenPhase(id, "restarting")
	if err := m.restartAgent(id, cols, rows); err != nil {
		if errors.Is(err, errArchived) {
			m.host.logger.Printf("regenerate for workspace %s cancelled by archive", id)
			return
		}
		m.host.logger.Printf("regenerate restart failed for workspace %s: %v", id, err)
		return
	}
	m.host.logger.Printf("regenerate: restarted ws=%s handoff=%v", id, handoff)
	if handoff {
		m.setRegenPhase(id, "seeding")
		// Arm a one-shot folder-trust approval only while the restarted agent boots.
		m.armWorkspaceTrustApproval(id, "regenerate-seed")
		m.waitWorkspaceReady(id, cancelled)
		m.clearWorkspaceTrustApproval(id)
		m.submitPrompt(id, seedPrompt)
		m.host.logger.Printf("regenerate: seed sent ws=%s", id)
		m.logScreenTail(id, "after-seed-send")
	}
}

func (m *workspaceManager) runHandoff(id string, regen *regenState, cancelled func() bool) {
	worktree := m.workspaceWorktree(id)
	if worktree == "" {
		return
	}
	handoffPath := filepath.Join(worktree, "HANDOFF.md")
	baseToken, _ := handoffFileToken(handoffPath)

	m.armWorkspaceTrustApproval(id, "regenerate-handoff")
	m.waitWorkspaceReady(id, cancelled)
	m.clearWorkspaceTrustApproval(id)
	if cancelled() {
		m.writeTranscriptFallback(id, handoffPath)
		return
	}
	m.submitPrompt(id, handoffPrompt)
	m.host.logger.Printf("regenerate handoff: prompt sent to ws=%s", id)
	m.logScreenTail(id, "after-handoff-send")

	start := time.Now()
	lastFileChange := start
	lastActivity := start
	lastToken := baseToken
	ticker := time.NewTicker(regenTick(m.thresholds))
	defer ticker.Stop()
	for {
		forced := cancelled()
		token, content := handoffFileToken(handoffPath)
		now := time.Now()
		if token != lastToken {
			lastToken = token
			lastFileChange = now
			lastActivity = now
		}
		busy, waiting := false, false
		if s := m.workspaceSession(id); s != nil {
			busy, waiting = s.agentStatus()
			if busy {
				lastActivity = now
			}
		}
		screen := ""
		if s := m.workspaceSession(id); s != nil {
			screen = s.capture(false, false)
		}
		snap := regenWait{
			sentinelSeen: hasSentinelLine(screen),
			fileChanged:  token != baseToken,
			fileStableMs: now.Sub(lastFileChange).Milliseconds(),
			agentBusy:    busy,
			agentWaiting: waiting,
			inactiveMs:   now.Sub(lastActivity).Milliseconds(),
			elapsedMs:    now.Sub(start).Milliseconds(),
			forced:       forced,
		}
		if proceed, reason := handoffReady(snap, m.thresholds); proceed {
			usable := snap.fileChanged && len(strings.TrimSpace(content)) >= 16
			m.host.logger.Printf("regenerate handoff: proceeding ws=%s reason=%s usable=%v fileChanged=%v elapsedMs=%d",
				id, reason, usable, snap.fileChanged, snap.elapsedMs)
			if !usable {
				m.writeTranscriptFallback(id, handoffPath)
			}
			return
		}
		select {
		case <-ticker.C:
		case <-regen.force:
		case <-m.host.shutdownCh:
		}
	}
}

func regenTick(th regenThresholds) time.Duration {
	tick := 500 * time.Millisecond
	min := th.stableMs
	for _, v := range []int64{th.graceMs, th.inactivityMs, th.hardCapMs} {
		if v > 0 && (min <= 0 || v < min) {
			min = v
		}
	}
	if min > 0 && min < 1000 {
		tick = time.Duration(maxInt64(10, min/2)) * time.Millisecond
	}
	return tick
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

// hasSentinelLine reports whether the agent printed the completion sentinel on a
// line of its own. It matches an exact trimmed line (not a substring) on the
// VISIBLE screen only, so the handoff prompt — which itself contains the word
// HANDOFF_COMPLETE ("…print: HANDOFF_COMPLETE") and is echoed/kept in scrollback —
// does not trigger a false early completion.
func hasSentinelLine(screen string) bool {
	for _, line := range strings.Split(screen, "\n") {
		if strings.TrimSpace(line) == handoffSentinel {
			return true
		}
	}
	return false
}

func handoffFileToken(path string) (token, content string) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "absent", ""
		}
		return "error:" + err.Error(), ""
	}
	sum := sha256.Sum256(data)
	return "present:" + hex.EncodeToString(sum[:]), string(data)
}

func (m *workspaceManager) writeTranscriptFallback(id, handoffPath string) {
	transcript := ""
	if s := m.workspaceSession(id); s != nil {
		transcript = s.capture(true, false)
	}
	_ = os.WriteFile(handoffPath, []byte("# Auto-captured transcript (agent did not write a handoff)\n\n"+transcript), 0o600)
}

// waitWorkspaceReady waits until a workspace's agent is ready to accept typed
// input: it has produced some output (a just-spawned CLI reads as "not busy"
// before it has rendered anything, so a plain not-busy check fires too early and
// the input is dropped during the boot stdin-flush) and has since settled to
// not-busy, after at least bootFloor. Bounded; returns on cancellation.
func (m *workspaceManager) waitWorkspaceReady(id string, cancelled func() bool) {
	start := time.Now()
	sawOutput := false
	deadline := start.Add(25 * time.Second)
	ticker := time.NewTicker(150 * time.Millisecond)
	defer ticker.Stop()
	for {
		if cancelled != nil && cancelled() {
			return
		}
		if s := m.workspaceSession(id); s != nil {
			busy, _ := s.agentStatus()
			if busy || strings.TrimSpace(s.capture(false, false)) != "" {
				sawOutput = true
			}
			if time.Since(start) >= m.bootFloor && sawOutput && !busy {
				return
			}
		}
		if time.Now().After(deadline) {
			return
		}
		select {
		case <-ticker.C:
		case <-m.host.shutdownCh:
			return
		}
	}
}

func (m *workspaceManager) setRegenPhase(id, phase string) {
	m.mu.Lock()
	if regen := m.regens[id]; regen != nil {
		regen.phase = phase
	}
	m.mu.Unlock()
}

func (m *workspaceManager) workspaceSession(id string) managedSession {
	m.mu.Lock()
	w := m.wss[id]
	m.mu.Unlock()
	if w == nil {
		return nil
	}
	s, _ := m.host.getSession(w.SessionName)
	return s
}

func (m *workspaceManager) workspaceWorktree(id string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if w := m.wss[id]; w != nil {
		return w.WorktreePath
	}
	return ""
}

func (m *workspaceManager) armWorkspaceTrustApproval(id, reason string) {
	if s := m.workspaceSession(id); s != nil {
		s.armTrustApproval(reason, time.Now().Add(trustApprovalTTL))
	}
}

func (m *workspaceManager) clearWorkspaceTrustApproval(id string) {
	if s := m.workspaceSession(id); s != nil {
		s.clearTrustApproval()
	}
}

// submitPrompt types text into the agent's input box and submits it. The text and
// the submit Enter are sent as SEPARATE writes with a settle delay between them:
// a trailing CR in the same write as a long paste is coalesced into the agent's
// input as a newline instead of submitting, so the agent (copilot) only treats the
// CR as "send" once it arrives as its own keystroke after the pasted text has
// rendered. The Enter is retried until the agent starts working (goes busy), since
// the first CR can still be swallowed while the paste is mid-render.
func (m *workspaceManager) submitPrompt(id, text string) {
	s := m.workspaceSession(id)
	if s == nil {
		m.host.logger.Printf("regenerate: submitPrompt found no live session for ws=%s", id)
		return
	}
	oneLine := strings.Join(strings.Fields(text), " ")
	_ = s.sendKeys([]byte(oneLine))
	submitted := false
	for attempt := 0; attempt < 4; attempt++ {
		if m.submitDelay > 0 {
			time.Sleep(m.submitDelay)
		}
		_ = s.sendKeys([]byte{'\r'})
		if m.submitDelay > 0 {
			time.Sleep(m.submitDelay)
		}
		if busy, _ := s.agentStatus(); busy {
			submitted = true
			break
		}
	}
	m.host.logger.Printf("regenerate: submitPrompt ws=%s len=%d submitted=%v", id, len(oneLine), submitted)
}

// logScreenTail logs the last chunk of the agent's visible screen (whitespace
// collapsed) so a manual-QA run can show whether a prompt actually reached and was
// submitted by the agent.
func (m *workspaceManager) logScreenTail(id, label string) {
	s := m.workspaceSession(id)
	if s == nil {
		return
	}
	scr := strings.Join(strings.Fields(s.capture(false, false)), " ")
	if len(scr) > 180 {
		scr = scr[len(scr)-180:]
	}
	m.host.logger.Printf("regenerate %s ws=%s screen=%q", label, id, scr)
}

func (m *workspaceManager) restartAgent(id string, cols, rows int) error {
	var oldName, newName, program, worktree, agentSessionID, shell string
	var autoYes bool
	m.mu.Lock()
	w := m.wss[id]
	if m.tombstone[id] || w == nil {
		m.mu.Unlock()
		return errArchived
	}
	oldName = w.SessionName
	w.SessionName = "ws_" + id + "-" + shortRand()
	if agentcmd.SupportsResume(w.Program) {
		w.AgentSessionID = newUUID()
	} else {
		w.AgentSessionID = ""
	}
	newName, program, worktree, agentSessionID, autoYes, shell = w.SessionName, w.Program, w.WorktreePath, w.AgentSessionID, w.AutoYes, w.Shell
	if shell == "" {
		shell = "cmd"
	}
	m.saveLocked()
	m.mu.Unlock()

	oldKilled := false
	defer func() {
		if !oldKilled {
			m.host.killSession(oldName)
		}
	}()
	m.host.killSession(oldName)
	oldKilled = true

	var lastErr error
	for attempt := 0; attempt < 8; attempt++ {
		err := m.host.startManagedSessionWithShell(newName, agentcmd.SeedNewCommand(program, agentSessionID), worktree, shell, sizeOr(cols, 120), sizeOr(rows, 30), autoYes)
		if err == nil {
			return nil
		}
		lastErr = err
		if !strings.Contains(err.Error(), "session already exists") {
			return err
		}
		m.mu.Lock()
		w := m.wss[id]
		if m.tombstone[id] || w == nil {
			m.mu.Unlock()
			return errArchived
		}
		w.SessionName = "ws_" + id + "-" + shortRand()
		newName = w.SessionName
		m.saveLocked()
		m.mu.Unlock()
	}
	return lastErr
}

func (m *workspaceManager) create(req *proto.Request) *proto.Response {
	if req.RepoPath == "" {
		return proto.Errorf(req.ID, "repoPath required")
	}
	title := strings.TrimSpace(req.Title)
	if title == "" {
		title = defaultTitle(req.RepoPath)
	}
	cfg := config.LoadConfig()
	program := req.Program
	if program == "" {
		program = cfg.GetProgram()
	}
	shell := req.Shell
	if shell == "" {
		shell = cfg.DefaultShell
	}
	if shell == "" {
		shell = "cmd"
	}

	// Validate the agent program resolves *before* creating any worktree or
	// session. For cmd, check PATH; for powershell/pwsh, also probe Get-Command
	// (with profile loaded so functions like `cpa` are visible).
	argv, perr := agentcmd.ParseProgram(program)
	if perr != nil {
		return proto.Errorf(req.ID, "no agent program configured")
	}
	progName := argv[0]
	if _, err := exec.LookPath(progName); err != nil {
		if shell == "powershell" || shell == "pwsh" {
			psExe := "powershell.exe"
			if shell == "pwsh" {
				psExe = "pwsh.exe"
			}
			// The untrusted program name is bound via an environment variable and
			// referenced through Get-Command -Name $env:HANGAR_PROBE_NAME — never
			// string-interpolated — so a token like `x'); calc; ('` resolves to
			// "not found" instead of executing (F-05). -WindowStyle Hidden avoids
			// the blue PowerShell flash; no -NoProfile so $PROFILE functions are
			// visible to Get-Command.
			const probeScript = `if (Get-Command -Name $env:HANGAR_PROBE_NAME -ErrorAction SilentlyContinue) { exit 0 } else { exit 1 }`
			probe := exec.Command(psExe, "-WindowStyle", "Hidden", "-Command", probeScript)
			probe.Env = append(os.Environ(), "HANGAR_PROBE_NAME="+progName)
			hideConsole(probe)
			if probeErr := probe.Run(); probeErr != nil {
				return proto.Errorf(req.ID, "agent program %q not found on PATH or as a PowerShell command (set a valid agent such as 'copilot' or 'claude'): %v", progName, err)
			}
		} else {
			return proto.Errorf(req.ID, "agent program %q not found on PATH (set a valid agent such as 'copilot' or 'claude'): %v", progName, err)
		}
	}

	id := newWorkspaceID()
	sessionName := "ws_" + id
	gitName := slug(title) + "-" + id[:6] // unique branch even for duplicate titles

	// Give resumable agents (copilot) a stable session UUID so a relaunch after a
	// daemon restart continues the same conversation instead of starting fresh.
	agentSessionID := ""
	if agentcmd.SupportsResume(program) {
		agentSessionID = newUUID()
	}

	var (
		wt     *git.GitWorktree
		branch string
		err    error
	)
	if req.BaseBranch != "" {
		wt, err = git.NewGitWorktreeFromBranch(req.RepoPath, req.BaseBranch, gitName)
		branch = req.BaseBranch
	} else {
		wt, branch, err = git.NewGitWorktree(req.RepoPath, gitName)
	}
	if err != nil {
		return proto.Errorf(req.ID, "prepare worktree: %v", err)
	}
	if err := wt.Setup(); err != nil {
		return proto.Errorf(req.ID, "create worktree: %v", err)
	}

	cols, rows := sizeOr(req.Cols, 120), sizeOr(req.Rows, 30)
	if err := m.host.startManagedSessionWithShell(sessionName, agentcmd.SeedNewCommand(program, agentSessionID), wt.GetWorktreePath(), shell, cols, rows, req.AutoYes); err != nil {
		// Roll back the worktree so a failed create leaves no orphan.
		_ = wt.Remove()
		_ = wt.Prune()
		return proto.Errorf(req.ID, "start agent: %v", err)
	}

	w := &workspace{
		ID: id, Title: title, Program: program, RepoPath: wt.GetRepoPath(),
		WorktreePath: wt.GetWorktreePath(), Branch: branch, BaseSHA: wt.GetBaseCommitSHA(),
		SessionName: sessionName, AutoYes: req.AutoYes, ExistingBranch: req.BaseBranch != "",
		CreatedUnix: time.Now().Unix(), AgentSessionID: agentSessionID, Shell: shell,
	}
	m.mu.Lock()
	m.wss[id] = w
	info := m.toInfo(w)
	m.saveLocked()
	m.mu.Unlock()

	m.host.logger.Printf("created workspace %q (%s) branch=%s worktree=%s", title, id, branch, w.WorktreePath)
	return &proto.Response{ID: req.ID, OK: true, Workspace: &info}
}

func (m *workspaceManager) archive(req *proto.Request) *proto.Response {
	m.mu.Lock()
	w, ok := m.wss[req.WorkspaceID]
	if !ok {
		m.mu.Unlock()
		return proto.Errorf(req.ID, "no such workspace: %s", req.WorkspaceID)
	}
	if m.tombstone[w.ID] {
		m.mu.Unlock()
		return &proto.Response{ID: req.ID, OK: true, Content: "archive already in progress"}
	}
	var done chan struct{}
	if regen := m.regens[w.ID]; regen != nil {
		m.tombstone[w.ID] = true
		regen.closeOnce.Do(func() { close(regen.force) })
		done = regen.done
	}
	m.mu.Unlock()

	if done != nil {
		select {
		case <-done:
		case <-time.After(60 * time.Second):
		case <-m.host.shutdownCh:
		}
	}

	m.mu.Lock()
	w, ok = m.wss[req.WorkspaceID]
	if !ok {
		delete(m.tombstone, req.WorkspaceID)
		m.mu.Unlock()
		return &proto.Response{ID: req.ID, OK: true}
	}
	delete(m.wss, w.ID)
	delete(m.tombstone, w.ID)
	sessionName := w.SessionName
	m.saveLocked()
	m.mu.Unlock()

	// Three-phase archive:
	// 1. Stop agent: remove from manager + stop run + kill session
	// 2. Optional filesystem cleanup (if DeleteWorktree == true)
	// 3. Persist state

	// Phase 1: Stop the agent session
	m.host.runs.stop(w.ID)
	m.host.killSession(sessionName)

	// Phase 2: Optional worktree/branch deletion
	if req.DeleteWorktree {
		wt := w.worktreeFor()

		// Refuse to delete a worktree path that is not contained within the
		// managed worktrees directory. workspaces.json is untrusted on-disk
		// state, so a poisoned worktreePath must never reach a destructive
		// `git worktree remove`/RemoveAll here (F-09).
		if err := git.AssertWorktreePathContained(w.WorktreePath); err != nil {
			m.host.logger.Printf("SECURITY: refusing to delete uncontained worktree %q for workspace %s: %v", w.WorktreePath, w.ID, err)
			return proto.Errorf(req.ID, "refusing to delete unsafe worktree path: %v", err)
		}

		// Remove worktree directory (retry loop for Windows handle release)
		for i := 0; i < 10; i++ {
			if err := wt.Remove(); err == nil {
				break
			}
			time.Sleep(150 * time.Millisecond)
		}

		// Prune worktree references
		_ = wt.Prune()

		// Best-effort branch deletion (local only, non-fatal if fails)
		// We use git CLI directly here instead of worktree methods
		cmd := exec.Command("git", "-C", w.RepoPath, "branch", "-D", w.Branch)
		_ = cmd.Run() // Ignore errors - branch might not exist locally

		m.host.logger.Printf("archived workspace %q (%s) - deleted worktree and branch %s", w.Title, w.ID, w.Branch)
	} else {
		// Keep worktree and branch - just prune references
		wt := w.worktreeFor()
		_ = wt.Prune()
		m.host.logger.Printf("archived workspace %q (%s) - kept worktree and branch %s", w.Title, w.ID, w.Branch)
	}

	return &proto.Response{ID: req.ID, OK: true}
}

func (m *workspaceManager) diff(req *proto.Request) *proto.Response {
	m.mu.Lock()
	w, ok := m.wss[req.WorkspaceID]
	m.mu.Unlock()
	if !ok {
		return proto.Errorf(req.ID, "no such workspace: %s", req.WorkspaceID)
	}
	wt := w.worktreeFor()
	if req.File != "" {
		d, err := wt.FileDiff(req.File)
		if err != nil {
			return proto.Errorf(req.ID, "file diff: %v", err)
		}
		return &proto.Response{ID: req.ID, OK: true, Diff: d}
	}
	files, err := wt.ChangedFiles()
	if err != nil {
		return proto.Errorf(req.ID, "changed files: %v", err)
	}
	out := make([]proto.FileDiffInfo, 0, len(files))
	for _, f := range files {
		out = append(out, proto.FileDiffInfo{Path: f.Path, Added: f.Added, Removed: f.Removed})
	}
	return &proto.Response{ID: req.ID, OK: true, Files: out}
}

func runWorkspaceGit(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	hideConsole(cmd)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s: %s (%w)", strings.Join(args, " "), strings.TrimSpace(string(out)), err)
	}
	return string(out), nil
}

func (m *workspaceManager) commit(req *proto.Request) *proto.Response {
	message := strings.TrimSpace(req.Message)
	if message == "" {
		return proto.Errorf(req.ID, "commit message required")
	}

	m.mu.Lock()
	w, ok := m.wss[req.WorkspaceID]
	m.mu.Unlock()
	if !ok {
		return proto.Errorf(req.ID, "no such workspace: %s", req.WorkspaceID)
	}

	status, err := runWorkspaceGit(w.WorktreePath, "status", "--porcelain")
	if err != nil {
		return proto.Errorf(req.ID, "check workspace status: %v", err)
	}
	if strings.TrimSpace(status) == "" {
		return &proto.Response{ID: req.ID, OK: true, Content: "nothing to commit"}
	}
	if _, err := runWorkspaceGit(w.WorktreePath, "add", "-A", "."); err != nil {
		return proto.Errorf(req.ID, "stage workspace changes: %v", err)
	}
	if _, err := runWorkspaceGit(w.WorktreePath, "commit", "-m", message, "--no-verify"); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "nothing to commit") {
			return &proto.Response{ID: req.ID, OK: true, Content: "nothing to commit"}
		}
		return proto.Errorf(req.ID, "commit workspace changes: %v", err)
	}
	head, err := runWorkspaceGit(w.WorktreePath, "rev-parse", "HEAD")
	if err != nil {
		return proto.Errorf(req.ID, "read committed head: %v", err)
	}
	return &proto.Response{ID: req.ID, OK: true, Content: strings.TrimSpace(head)}
}

func (m *workspaceManager) push(req *proto.Request) *proto.Response {
	m.mu.Lock()
	w, ok := m.wss[req.WorkspaceID]
	m.mu.Unlock()
	if !ok {
		return proto.Errorf(req.ID, "no such workspace: %s", req.WorkspaceID)
	}

	if _, err := runWorkspaceGit(w.WorktreePath, "remote", "get-url", "origin"); err != nil {
		return proto.Errorf(req.ID, "workspace push requires an origin remote")
	}
	out, err := runWorkspaceGit(w.WorktreePath, "push", "-u", "origin", w.Branch)
	if err != nil {
		return proto.Errorf(req.ID, "push workspace branch: %v", err)
	}
	return &proto.Response{ID: req.ID, OK: true, Content: out}
}

func (m *workspaceManager) setAutoYes(req *proto.Request) *proto.Response {
	m.mu.Lock()
	w, ok := m.wss[req.WorkspaceID]
	if ok {
		w.AutoYes = req.Enabled
		m.saveLocked()
	}
	m.mu.Unlock()
	if !ok {
		return proto.Errorf(req.ID, "no such workspace: %s", req.WorkspaceID)
	}
	if s, ok := m.host.getSession(w.SessionName); ok {
		s.setAutoYes(req.Enabled)
	}
	return &proto.Response{ID: req.ID, OK: true}
}

func (m *workspaceManager) updateWorkspace(req *proto.Request) *proto.Response {
	m.mu.Lock()
	w, ok := m.wss[req.WorkspaceID]
	if !ok {
		m.mu.Unlock()
		return proto.Errorf(req.ID, "no such workspace: %s", req.WorkspaceID)
	}
	if t := strings.TrimSpace(req.Title); t != "" {
		w.Title = t
	}
	if req.Program != "" {
		w.Program = req.Program
	}
	if req.Shell != "" {
		w.Shell = req.Shell
	}
	m.saveLocked()
	info := m.toInfo(w)
	m.mu.Unlock()
	return &proto.Response{ID: req.ID, OK: true, Workspace: &info}
}

func (m *workspaceManager) listCopilotSessions(req *proto.Request) *proto.Response {
	sessions, skipped, err := copilot.DiscoverWithStats()
	if err != nil {
		return proto.Errorf(req.ID, "discover copilot sessions: %v", err)
	}

	// Check which session IDs are already resumed as workspaces.
	m.mu.Lock()
	resumedIDs := make(map[string]bool)
	for _, w := range m.wss {
		if w.AgentSessionID != "" {
			resumedIDs[w.AgentSessionID] = true
		}
	}
	m.mu.Unlock()

	out := make([]proto.CopilotSessionInfo, 0, len(sessions))
	for _, s := range sessions {
		inUse := s.InUse || resumedIDs[s.ID]
		firstMsg, _ := copilot.FirstUserMessage(s)
		out = append(out, proto.CopilotSessionInfo{
			ID:         s.ID,
			Name:       s.DisplayName(),
			Repository: s.Repository,
			Branch:     s.Branch,
			OriginRoot: s.OriginRoot,
			CreatedAt:  s.CreatedAt.Unix(),
			UpdatedAt:  s.UpdatedAt.Unix(),
			InUse:      inUse,
			FirstMsg:   firstMsg,
		})
	}
	return &proto.Response{ID: req.ID, OK: true, CopilotSessions: out, Skipped: skipped}
}

func (m *workspaceManager) resumeCopilotSession(req *proto.Request) *proto.Response {
	if req.SessionID == "" {
		return proto.Errorf(req.ID, "sessionId required")
	}
	// req.SessionID arrives over the host pipe from the TUI/desktop client. The
	// pipe is the trust boundary, so validate server-side rather than relying on
	// the client; an invalid id can never reach the resume command line (F-01).
	if !agentcmd.ValidSessionID(req.SessionID) {
		return proto.Errorf(req.ID, "invalid session id")
	}

	// The resume flow reuses the normal workspace creation but sets the agent
	// session ID so the agent resumes the conversation instead of starting fresh.
	// We need the repo path — use the request's repoPath or fall back to cwd.
	repoPath := req.RepoPath
	if repoPath == "" {
		var err error
		repoPath, err = os.Getwd()
		if err != nil {
			return proto.Errorf(req.ID, "cannot determine repo path: %v", err)
		}
	}

	// F-03: the repoPath/OriginRoot arrives over the host pipe from the
	// TUI/desktop client. The pipe is the trust boundary, so we enforce server
	// side — never relying on the client — that:
	//   1. the target is a real local git repository (so `git worktree add` only
	//      ever runs a post-checkout hook from a genuine repo the user owns), and
	//   2. resuming into a repo OTHER than the host's own working directory is
	//      explicitly confirmed (the desktop client previously sent originRoot
	//      with no confirmation at all).
	absRepo, err := filepath.Abs(repoPath)
	if err != nil {
		return proto.Errorf(req.ID, "invalid repo path %q: %v", repoPath, err)
	}
	absRepo = filepath.Clean(absRepo)
	if !git.IsLocalGitRepo(absRepo) {
		return proto.Errorf(req.ID, "repo path %q is not a local git repository", absRepo)
	}
	cwd, _ := os.Getwd()
	absCwd, _ := filepath.Abs(cwd)
	crossRepo := !strings.EqualFold(filepath.Clean(absCwd), absRepo)
	if crossRepo && !req.Confirmed {
		m.host.logger.Printf("resumeCopilotSession: cross-repo resume into %q requires confirmation", absRepo)
		return &proto.Response{ID: req.ID, OK: false, NeedsConfirm: true, AbsPath: absRepo}
	}
	repoPath = absRepo

	cfg := config.LoadConfig()
	// These are Copilot sessions — always use "copilot" for the resume command
	// (even if the user's default program is a wrapper like "cpa"). The workspace
	// stores the user's configured program so regenerate uses their preference.
	resumeProgram := "copilot"
	displayProgram := cfg.GetProgram()

	shell := req.Shell
	if shell == "" {
		shell = cfg.DefaultShell
	}
	if shell == "" {
		shell = "cmd"
	}

	title := req.Title
	if title == "" {
		title = "Resumed session"
	}

	id := newWorkspaceID()
	sessionName := "ws_" + id
	gitName := slug(title) + "-" + id[:6]

	wt, branch, err := git.NewGitWorktree(repoPath, gitName)
	if err != nil {
		return proto.Errorf(req.ID, "prepare worktree: %v", err)
	}
	if err := wt.Setup(); err != nil {
		return proto.Errorf(req.ID, "create worktree: %v", err)
	}

	cols, rows := sizeOr(req.Cols, 120), sizeOr(req.Rows, 30)
	if err := m.host.startManagedSessionWithShell(sessionName, agentcmd.ResumeCommand(resumeProgram, req.SessionID), wt.GetWorktreePath(), shell, cols, rows, req.AutoYes); err != nil {
		_ = wt.Remove()
		_ = wt.Prune()
		return proto.Errorf(req.ID, "start agent: %v", err)
	}

	w := &workspace{
		ID: id, Title: title, Program: displayProgram, RepoPath: wt.GetRepoPath(),
		WorktreePath: wt.GetWorktreePath(), Branch: branch, BaseSHA: wt.GetBaseCommitSHA(),
		SessionName: sessionName, AutoYes: req.AutoYes,
		CreatedUnix: time.Now().Unix(), AgentSessionID: req.SessionID, Shell: shell,
	}
	m.mu.Lock()
	m.wss[id] = w
	info := m.toInfo(w)
	m.saveLocked()
	m.mu.Unlock()

	m.host.logger.Printf("resumed copilot session %q as workspace %q (%s)", req.SessionID, title, id)
	return &proto.Response{ID: req.ID, OK: true, Workspace: &info}
}

func (m *workspaceManager) startRun(req *proto.Request) *proto.Response {
	command := strings.TrimSpace(req.Command)
	if command == "" {
		return proto.Errorf(req.ID, "run command required")
	}

	m.mu.Lock()
	w, ok := m.wss[req.WorkspaceID]
	m.mu.Unlock()
	if !ok {
		return proto.Errorf(req.ID, "no such workspace: %s", req.WorkspaceID)
	}

	if err := m.host.runs.start(w.ID, w.WorktreePath, command); err != nil {
		return proto.Errorf(req.ID, "start run: %v", err)
	}

	m.mu.Lock()
	if current, ok := m.wss[w.ID]; ok {
		current.RunCommand = command
		m.saveLocked()
	}
	m.mu.Unlock()
	return &proto.Response{ID: req.ID, OK: true}
}

func (m *workspaceManager) stopRun(req *proto.Request) *proto.Response {
	m.mu.Lock()
	_, ok := m.wss[req.WorkspaceID]
	m.mu.Unlock()
	if !ok {
		return proto.Errorf(req.ID, "no such workspace: %s", req.WorkspaceID)
	}
	m.host.runs.stop(req.WorkspaceID)
	return &proto.Response{ID: req.ID, OK: true}
}

func (m *workspaceManager) runOutput(req *proto.Request) *proto.Response {
	m.mu.Lock()
	_, ok := m.wss[req.WorkspaceID]
	m.mu.Unlock()
	if !ok {
		return proto.Errorf(req.ID, "no such workspace: %s", req.WorkspaceID)
	}
	data, nextOffset, running, exitCode := m.host.runs.output(req.WorkspaceID, req.SinceOffset)
	return &proto.Response{
		ID: req.ID, OK: true, Data: data, NextOffset: nextOffset,
		RunRunning: running, ExitCode: exitCode,
	}
}
