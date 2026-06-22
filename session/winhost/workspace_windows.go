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
	"strconv"
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
	AgentSessionID string `json:"agentSessionId"`          // stable agent session UUID for resume (copilot)
	Shell          string `json:"shell,omitempty"`         // "cmd", "powershell", "pwsh"; empty = config default
	CopilotResume  bool   `json:"copilotResume,omitempty"` // agent is copilot or a detected copilot wrapper (e.g. "cpa") -> resumable
	NoWorktree     bool   `json:"noWorktree,omitempty"`    // in-place session: opened directly against RepoPath, no managed worktree
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
	confirmWait time.Duration // wait after the submit Enter before sampling for submission (must clear the input-echo window)
	chunkDelay  time.Duration // gap between keystroke chunks when typing into a non-bracketed-paste agent

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
		confirmWait: promptConfirmWait,
		chunkDelay:  promptChunkDelay,
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
		if w.WorktreePath != "" && !w.NoWorktree {
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

// promptConfirmWait is how long to wait after the submit Enter before sampling
// whether the agent accepted it. It MUST exceed statusInputEchoMs (600ms) so the
// agent's first post-submit output is recorded as activity rather than suppressed
// as our own input echoing (which would make submission undetectable — see
// conpty_windows.go updateStatusFrom / agentStatus).
const promptConfirmWait = 750 * time.Millisecond

// promptChunkDelay is the gap between keystroke chunks when typing a prompt into an
// agent that has NOT enabled bracketed paste, so the CLI sees incremental typing
// rather than one burst its input editor may mishandle (dropping the trailing CR).
const promptChunkDelay = 15 * time.Millisecond

// promptChunkRunes bounds each keystroke chunk (in runes) for the non-bracketed
// "typed" fallback. Chunking never splits a rune.
const promptChunkRunes = 48

// Bracketed-paste markers (DEC 2004). When the agent has bracketed paste enabled,
// the prompt text is framed so the CLI inserts it as one block and the following CR
// submits it — the same contract the desktop xterm uses (TermView.tsx). The submit
// CR is ALWAYS sent as a separate write, never inside the markers.
const (
	bracketedPasteStart = "\x1b[200~"
	bracketedPasteEnd   = "\x1b[201~"
)

// focusInSeq is the terminal focus-in report (DEC mode 1004). The daemon sends it
// before submitting a prompt so a focus-reporting CLI (copilot) treats its terminal
// as focused and will submit on Enter; without it, a prior focus-out (emitted by the
// desktop xterm when another pane/modal took focus) leaves the agent accepting typed
// text but refusing to submit. Disable with HANGAR_SUBMIT_FOCUS=0.
const focusInSeq = "\x1b[I"

// submitEnterAttempts bounds how many times the submit Enter is (re)sent while
// waiting for the agent to accept the prompt.
const submitEnterAttempts = 3

const trustApprovalTTL = 15 * time.Second

const (
	// handoffPrompt and seedPrompt are SINGLE-LINE on purpose: the non-bracketed
	// "typed" fallback would send an embedded \n as a literal newline in the agent's
	// editor and the trailing Enter would then add a line instead of submitting. One
	// line + one separate Enter is the submit contract the desktop terminal uses.
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
	wt := git.NewGitWorktreeFromStorage(w.RepoPath, w.WorktreePath, w.SessionName, w.Branch, w.BaseSHA, w.ExistingBranch)
	// In-place sessions run diffs against the user's real repo, so the diff
	// helpers must not mutate/lock its index with `git add -N`.
	wt.SetNoStage(w.NoWorktree)
	return wt
}

// inPlaceGitInfo returns the repo toplevel, current branch, and HEAD SHA for an
// in-place session's folder. All three are empty when dir is not inside a git
// work tree (a detached HEAD also yields an empty branch), in which case the
// caller opens the session with git features disabled.
func inPlaceGitInfo(dir string) (repoPath, branch, baseSHA string) {
	top, err := runWorkspaceGit(dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", "", ""
	}
	repoPath = filepath.Clean(strings.TrimSpace(top))
	if b, err := runWorkspaceGit(dir, "rev-parse", "--abbrev-ref", "HEAD"); err == nil {
		if bn := strings.TrimSpace(b); bn != "HEAD" {
			branch = bn
		}
	}
	if s, err := runWorkspaceGit(dir, "rev-parse", "HEAD"); err == nil {
		baseSHA = strings.TrimSpace(s)
	}
	return repoPath, branch, baseSHA
}

func (m *workspaceManager) toInfo(w *workspace) proto.WorkspaceInfo {
	alive := false
	busy, waiting := false, false
	var lastOutMs int64
	if s, ok := m.host.getSession(w.SessionName); ok {
		alive = s.alive()
		busy, waiting = s.agentStatus()
		lastOutMs = s.lastOutputUnixMs()
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
		LastOutputUnix: lastOutMs / 1000, // UnixMilli -> Unix seconds; 0 when no live session/no output
		RunCommand:     w.RunCommand, Running: running, PreviewURL: previewURL,
		Busy: busy, Waiting: waiting,
		Regenerating: regenerating, RegenPhase: phase, Shell: w.Shell,
		HasWorktree: !w.NoWorktree,
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
	if w.copilotResumable() && w.AgentSessionID != "" {
		if agentcmd.ValidSessionID(w.AgentSessionID) {
			program = agentcmd.ResumeFlagCommand(w.Program, w.AgentSessionID)
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

// submitPrompt types text into the agent's input box and submits it, then reports
// the injection method used and whether the agent accepted the prompt.
//
// The text and the submit Enter are ALWAYS sent as separate writes. How the text is
// typed depends on the agent's terminal state, mirroring the desktop terminal's
// proven contract (TermView.tsx):
//   - bracketed paste (DEC 2004) enabled  -> frame the text in paste markers so the
//     CLI inserts it as one block; the following separate CR submits it.
//   - otherwise                           -> type it in small chunks so the CLI sees
//     incremental keystrokes, not one burst whose trailing CR it would drop.
//
// A raw single-burst write + bare CR (the previous behavior, still available via
// HANGAR_SUBMIT_MODE=burst for diagnostics) does NOT reliably submit into Ink/React
// CLIs like copilot and claude — the prompt is left sitting in the input box.
func (m *workspaceManager) submitPrompt(id, text string) (method string, submitted bool) {
	s := m.workspaceSession(id)
	if s == nil {
		m.host.logger.Printf("regenerate: submitPrompt found no live session for ws=%s", id)
		return "", false
	}
	oneLine := strings.Join(strings.Fields(text), " ")
	bracketed := s.bracketedPasteEnabled()
	method = resolveSubmitMethod(bracketed, os.Getenv("HANGAR_SUBMIT_MODE"))
	enter := chooseEnterBytes(os.Getenv("HANGAR_SUBMIT_ENTER"))
	focus := os.Getenv("HANGAR_SUBMIT_FOCUS") != "0"

	// Tell the agent its terminal is focused before submitting. The desktop xterm
	// emits a focus-out (ESC[O) when the Regenerate modal/another pane takes focus,
	// and focus-reporting CLIs (copilot) then ACCEPT typed text but REFUSE to submit
	// on Enter until they see a focus-in (ESC[I) — which a manual click sends. A
	// freshly-booted agent (the seed target) never got a focus-out, which is why the
	// seed submitted while the live-agent handoff did not. The submit Enter itself is
	// a bare CR — exactly what the desktop xterm sends — so the byte was never the
	// problem; only the focus context differed.
	if focus {
		_ = s.sendKeys([]byte(focusInSeq))
	}

	for _, w := range submitWrites(oneLine, method, promptChunkRunes) {
		_ = s.sendKeys(w)
		if method == submitChunk && m.chunkDelay > 0 {
			time.Sleep(m.chunkDelay)
		}
	}

	confirm := m.effectiveConfirmWait()
	baseline := s.lastOutputUnixMs()
	for attempt := 0; attempt < submitEnterAttempts; attempt++ {
		if m.submitDelay > 0 {
			time.Sleep(m.submitDelay)
		}
		if focus {
			_ = s.sendKeys([]byte(focusInSeq))
		}
		_ = s.sendKeys(enter)
		if confirm > 0 {
			time.Sleep(confirm)
		}
		// The agent accepted the prompt if it started producing output AFTER the
		// echo window. busy and an advanced lastOutput are both side-effect-free and
		// (unlike a raw busy check right after the keystroke) not masked as our own
		// input echoing, because we waited past statusInputEchoMs.
		if busy, _ := s.agentStatus(); busy {
			submitted = true
			break
		}
		if s.lastOutputUnixMs() != baseline {
			submitted = true
			break
		}
	}
	m.host.logger.Printf("regenerate: submitPrompt ws=%s len=%d method=%s bracketed=%v focus=%v submitted=%v",
		id, len(oneLine), method, bracketed, focus, submitted)
	return method, submitted
}

// chooseEnterBytes selects the keystroke(s) used to submit a prompt. Default is a
// bare CR (\r) — verified to be exactly what the desktop xterm sends for Enter, and
// what submits when a user presses it manually. The HANGAR_SUBMIT_ENTER override
// (cr|lf|crlf) forces another line ending for an agent that might need it.
func chooseEnterBytes(override string) []byte {
	switch strings.ToLower(strings.TrimSpace(override)) {
	case "lf", "n", "\n":
		return []byte{'\n'}
	case "crlf":
		return []byte{'\r', '\n'}
	default:
		return []byte{'\r'}
	}
}

// effectiveConfirmWait is confirmWait with an optional HANGAR_SUBMIT_SETTLE_MS
// diagnostic override (milliseconds).
func (m *workspaceManager) effectiveConfirmWait() time.Duration {
	if v := strings.TrimSpace(os.Getenv("HANGAR_SUBMIT_SETTLE_MS")); v != "" {
		if ms, err := strconv.Atoi(v); err == nil && ms >= 0 {
			return time.Duration(ms) * time.Millisecond
		}
	}
	return m.confirmWait
}

const (
	submitPasteMode = "paste"
	submitChunk     = "chunk"
	submitBurst     = "burst"
)

// resolveSubmitMethod picks how to inject a prompt. An explicit HANGAR_SUBMIT_MODE
// override (paste|chunk|burst) wins for diagnostics; otherwise the method follows
// the agent's runtime bracketed-paste state, which keeps it agent-agnostic (it keys
// off the terminal mode the agent set, not the program name).
func resolveSubmitMethod(bracketed bool, override string) string {
	switch strings.ToLower(strings.TrimSpace(override)) {
	case submitPasteMode:
		return submitPasteMode
	case submitChunk:
		return submitChunk
	case submitBurst:
		return submitBurst
	}
	if bracketed {
		return submitPasteMode
	}
	return submitChunk
}

// submitWrites returns the keystroke writes that type oneLine into the agent's
// input box for the given method, WITHOUT the trailing submit CR (the caller sends
// that separately so the CLI commits the paste/typing first).
func submitWrites(oneLine, method string, chunkRunes int) [][]byte {
	switch method {
	case submitPasteMode:
		return [][]byte{[]byte(bracketedPasteStart + oneLine + bracketedPasteEnd)}
	case submitBurst:
		return [][]byte{[]byte(oneLine)}
	default: // chunk
		out := [][]byte{}
		for _, c := range chunkString(oneLine, chunkRunes) {
			out = append(out, []byte(c))
		}
		return out
	}
}

// chunkString splits s into substrings of at most n runes each (never splitting a
// multi-byte rune). n <= 0 or a short string returns s unsplit.
func chunkString(s string, n int) []string {
	if n <= 0 {
		return []string{s}
	}
	r := []rune(s)
	if len(r) <= n {
		return []string{s}
	}
	var out []string
	for len(r) > 0 {
		k := n
		if k > len(r) {
			k = len(r)
		}
		out = append(out, string(r[:k]))
		r = r[k:]
	}
	return out
}

// logScreenTail logs the agent's bottom visible rows (where the input box is) plus
// its bracketed-paste state, so a manual-QA run can see whether a prompt actually
// landed in the box and whether it cleared after submit.
func (m *workspaceManager) logScreenTail(id, label string) {
	s := m.workspaceSession(id)
	if s == nil {
		return
	}
	m.host.logger.Printf("regenerate %s ws=%s bracketed=%v tail=%q",
		label, id, s.bracketedPasteEnabled(), tailRows(s.capture(false, false), 6))
}

// tailRows returns the last n non-blank rows of a captured screen, joined by " | "
// so a single log line shows the input region (the bottom-most rows) instead of the
// status bar that a flat character tail would surface.
func tailRows(screen string, n int) string {
	lines := strings.Split(screen, "\n")
	trimmed := make([]string, 0, len(lines))
	for _, ln := range lines {
		if strings.TrimSpace(ln) != "" {
			trimmed = append(trimmed, strings.TrimRight(ln, " "))
		}
	}
	if n > 0 && len(trimmed) > n {
		trimmed = trimmed[len(trimmed)-n:]
	}
	return strings.Join(trimmed, " | ")
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
	if w.copilotResumable() {
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
		err := m.host.startManagedSessionWithShell(newName, agentcmd.SeedFlagCommand(program, agentSessionID), worktree, shell, sizeOr(cols, 120), sizeOr(rows, 30), autoYes)
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

// copilotResumable reports whether this workspace's agent supports copilot-style
// resume. CopilotResume is detected and persisted at create/import time and covers
// copilot wrappers such as `cpa` (whose name is not "copilot"). The SupportsResume
// fallback keeps workspaces created before this field existed (literal `copilot`)
// resuming across an upgrade.
func (w *workspace) copilotResumable() bool {
	return w.CopilotResume || agentcmd.SupportsResume(w.Program)
}

// copilotProbeScript resolves the agent named in $env:HANGAR_PROBE_NAME via
// Get-Command (with the user's $PROFILE loaded, so functions like `cpa` are
// visible) and reports, through its exit code, whether it exists and ultimately
// invokes copilot. The name is bound through the environment and never string-
// interpolated, so a hostile token cannot inject (F-05). Detection walks the
// parsed AST, so only real command invocations count — not comments or strings.
//
//	exit 0 = found and copilot-backed
//	exit 1 = found but not copilot
//	exit 2 = not found
const copilotProbeScript = `
$c = Get-Command -Name $env:HANGAR_PROBE_NAME -ErrorAction SilentlyContinue
if (-not $c) { exit 2 }
if ($c.CommandType -eq 'Alias' -and $c.ResolvedCommand) { $c = $c.ResolvedCommand }
$isCopilot = $false
try {
  if ($c.CommandType -eq 'Function' -or $c.CommandType -eq 'Filter') {
    $cmds = $c.ScriptBlock.Ast.FindAll({ param($n) $n -is [System.Management.Automation.Language.CommandAst] }, $true)
    foreach ($cmd in $cmds) {
      $name = $cmd.GetCommandName()
      if ($name -and ([System.IO.Path]::GetFileNameWithoutExtension($name) -ieq 'copilot')) { $isCopilot = $true; break }
    }
  } elseif ($c.CommandType -eq 'Application') {
    if ([System.IO.Path]::GetFileNameWithoutExtension([string]$c.Source) -ieq 'copilot') { $isCopilot = $true }
  }
} catch { }
if ($isCopilot) { exit 0 } else { exit 1 }
`

// agentProbeTimeout bounds how long probeAgentProgram waits for the PowerShell
// probe. The probe loads the user's PowerShell profile (so profile functions like
// `cpa` are visible), and on locked-down machines that profile can hang or be very
// slow (EDR, slow/network module loads). Without a bound, the probe — and thus the
// whole CreateWorkspace RPC — blocks forever, leaving the desktop "Creating…" modal
// stuck. It is a package var so tests can shrink it.
var agentProbeTimeout = 30 * time.Second

// probeAgentProgramTimed resolves an agent program that is not on PATH (e.g. a
// PowerShell profile function like `cpa`) and reports whether it exists, whether
// it is copilot-backed, and whether the probe exceeded agentProbeTimeout so the
// caller can log a hung probe (vs. a genuine "not found"). The probe is run under
// a context deadline so a hung PowerShell profile cannot wedge CreateWorkspace.
// A launch failure of the probe itself is treated as "not found".
func probeAgentProgramTimed(shell, progName string) (found, isCopilot, timedOut bool) {
	psExe := "powershell.exe"
	if shell == "pwsh" {
		psExe = "pwsh.exe"
	}
	ctx, cancel := context.WithTimeout(context.Background(), agentProbeTimeout)
	defer cancel()
	probe := exec.CommandContext(ctx, psExe, "-WindowStyle", "Hidden", "-Command", copilotProbeScript)
	probe.Env = append(os.Environ(), "HANGAR_PROBE_NAME="+progName)
	hideConsole(probe)
	err := probe.Run()
	if ctx.Err() == context.DeadlineExceeded {
		return false, false, true
	}
	found, isCopilot = classifyProbeExit(err)
	return found, isCopilot, false
}

// classifyProbeExit maps the probe process result to (found, isCopilot):
// exit 0 = found and copilot-backed; exit 1 = found, not copilot; exit 2 (or any
// launch failure) = not found.
func classifyProbeExit(err error) (found, isCopilot bool) {
	if err == nil {
		return true, true // exit 0
	}
	if ee, ok := err.(*exec.ExitError); ok {
		switch ee.ExitCode() {
		case 1:
			return true, false
		case 2:
			return false, false
		}
	}
	return false, false
}

func (m *workspaceManager) create(req *proto.Request) *proto.Response {
	if req.RepoPath == "" {
		return proto.Errorf(req.ID, "repoPath required")
	}
	// Per-phase timing so a "stuck on Creating…" report shows exactly which step
	// stalls (e.g. a slow `git worktree add` on a OneDrive/EDR-backed filesystem,
	// or a slow agent launch). The host processes a connection's RPCs serially, so
	// a slow create also delays the client's polling — these logs disambiguate.
	t0 := time.Now()
	lap := t0
	phase := func(label string) {
		now := time.Now()
		m.host.logger.Printf("create phase=%s took=%s total=%s repoPath=%q", label, now.Sub(lap).Round(time.Millisecond), now.Sub(t0).Round(time.Millisecond), req.RepoPath)
		lap = now
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
	// (with profile loaded so functions like `cpa` are visible). The same probe
	// reports whether the program ultimately invokes copilot, so a wrapper such as
	// `cpa` gets a resumable session id even though its name isn't "copilot".
	argv, perr := agentcmd.ParseProgram(program)
	if perr != nil {
		return proto.Errorf(req.ID, "no agent program configured")
	}
	progName := argv[0]
	copilotResume := agentcmd.SupportsResume(program)
	if _, err := exec.LookPath(progName); err != nil {
		if shell == "powershell" || shell == "pwsh" {
			found, isCopilot, timedOut := probeAgentProgramTimed(shell, progName)
			if timedOut {
				m.host.logger.Printf("agent probe timed out after %s for program %q (shell=%s); a slow/hung PowerShell profile can cause this — workspace create will report the agent as not found", agentProbeTimeout, progName, shell)
			}
			if !found {
				return proto.Errorf(req.ID, "agent program %q not found on PATH or as a PowerShell command (set a valid agent such as 'copilot' or 'claude'): %v", progName, err)
			}
			if isCopilot {
				copilotResume = true
			}
		} else {
			return proto.Errorf(req.ID, "agent program %q not found on PATH (set a valid agent such as 'copilot' or 'claude'): %v", progName, err)
		}
	}
	phase("validate-program")

	id := newWorkspaceID()
	sessionName := "ws_" + id
	gitName := slug(title) + "-" + id[:6] // unique branch even for duplicate titles

	// Give resumable agents (copilot, or a detected copilot wrapper such as `cpa`)
	// a stable session UUID so a relaunch after a daemon restart continues the same
	// conversation instead of starting fresh.
	agentSessionID := ""
	if copilotResume {
		agentSessionID = newUUID()
	}

	cols, rows := sizeOr(req.Cols, 120), sizeOr(req.Rows, 30)

	var (
		repoPath, worktreePath, branch, baseSHA string
		existingBranch                          bool
	)

	if req.NoWorktree {
		// In-place session: open the agent directly in the selected folder, no
		// managed worktree. Git features (diff/commit/push, branch label) come
		// from the folder's repo when it is one; a non-repo folder still opens.
		dir, derr := filepath.Abs(req.RepoPath)
		if derr != nil {
			return proto.Errorf(req.ID, "resolve folder: %v", derr)
		}
		if fi, serr := os.Stat(dir); serr != nil || !fi.IsDir() {
			return proto.Errorf(req.ID, "folder not found: %s", req.RepoPath)
		}
		worktreePath = dir
		repoPath, branch, baseSHA = inPlaceGitInfo(dir)
		if repoPath == "" {
			repoPath = dir
		}
		phase("resolve-folder")
		if err := m.host.startManagedSessionWithShell(sessionName, agentcmd.SeedFlagCommand(program, agentSessionID), worktreePath, shell, cols, rows, req.AutoYes); err != nil {
			return proto.Errorf(req.ID, "start agent: %v", err)
		}
		phase("start-agent")
	} else {
		var (
			wt  *git.GitWorktree
			err error
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
		phase("prepare-worktree")
		if err := wt.Setup(); err != nil {
			return proto.Errorf(req.ID, "create worktree: %v", err)
		}
		phase("setup-worktree")

		if err := m.host.startManagedSessionWithShell(sessionName, agentcmd.SeedFlagCommand(program, agentSessionID), wt.GetWorktreePath(), shell, cols, rows, req.AutoYes); err != nil {
			// Roll back the worktree so a failed create leaves no orphan.
			_ = wt.Remove()
			_ = wt.Prune()
			return proto.Errorf(req.ID, "start agent: %v", err)
		}
		phase("start-agent")
		repoPath, worktreePath, baseSHA = wt.GetRepoPath(), wt.GetWorktreePath(), wt.GetBaseCommitSHA()
		existingBranch = req.BaseBranch != ""
	}

	w := &workspace{
		ID: id, Title: title, Program: program, RepoPath: repoPath,
		WorktreePath: worktreePath, Branch: branch, BaseSHA: baseSHA,
		SessionName: sessionName, AutoYes: req.AutoYes, ExistingBranch: existingBranch,
		CreatedUnix: time.Now().Unix(), AgentSessionID: agentSessionID, Shell: shell,
		CopilotResume: copilotResume, NoWorktree: req.NoWorktree,
	}
	m.mu.Lock()
	m.wss[id] = w
	info := m.toInfo(w)
	m.saveLocked()
	m.mu.Unlock()

	m.host.logger.Printf("created workspace %q (%s) branch=%s worktree=%s total=%s", title, id, branch, w.WorktreePath, time.Since(t0).Round(time.Millisecond))
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

	// Phase 2: Optional worktree/branch deletion. An in-place session never owns
	// a managed worktree or branch (WorktreePath is the user's folder), so we must
	// never delete anything for it — archiving only stops the agent.
	switch {
	case w.NoWorktree:
		m.host.logger.Printf("archived in-place workspace %q (%s) - left folder %s untouched", w.Title, w.ID, w.WorktreePath)
	case req.DeleteWorktree:
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
	default:
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

	if w.NoWorktree && w.Branch == "" {
		return proto.Errorf(req.ID, "this in-place session is not in a git repository")
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

	if w.NoWorktree && w.Branch == "" {
		return proto.Errorf(req.ID, "this in-place session is not in a git repository")
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
		CopilotResume: true,
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
