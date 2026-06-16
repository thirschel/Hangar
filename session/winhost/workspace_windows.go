//go:build windows

package winhost

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"claude-squad/config"
	"claude-squad/session/git"
	"claude-squad/session/winhost/proto"
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
}

type workspaceManager struct {
	mu   sync.Mutex
	host *host
	wss  map[string]*workspace // by ID
}

func newWorkspaceManager(h *host) *workspaceManager {
	m := &workspaceManager{host: h, wss: map[string]*workspace{}}
	m.load()
	return m
}

// --- persistence (workspaces.json next to the rest of ~/.claude-squad state) ---

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

// worktreeFor reconstructs a GitWorktree handle from stored metadata so we can
// run diff/remove without re-resolving paths.
func (w *workspace) worktreeFor() *git.GitWorktree {
	return git.NewGitWorktreeFromStorage(w.RepoPath, w.WorktreePath, w.SessionName, w.Branch, w.BaseSHA, w.ExistingBranch)
}

func (m *workspaceManager) toInfo(w *workspace) proto.WorkspaceInfo {
	alive := false
	if s, ok := m.host.getSession(w.SessionName); ok {
		alive = s.alive()
	}
	added, removed := 0, 0
	if stats := w.worktreeFor().DiffNumstat(); stats != nil && stats.Error == nil {
		added, removed = stats.Added, stats.Removed
	}
	return proto.WorkspaceInfo{
		ID: w.ID, Title: w.Title, Program: w.Program, RepoPath: w.RepoPath,
		WorktreePath: w.WorktreePath, Branch: w.Branch, SessionName: w.SessionName,
		Alive: alive, AutoYes: w.AutoYes, Added: added, Removed: removed, CreatedUnix: w.CreatedUnix,
	}
}

// --- RPC handlers ---

func (m *workspaceManager) list(req *proto.Request) *proto.Response {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]proto.WorkspaceInfo, 0, len(m.wss))
	for _, w := range m.wss {
		out = append(out, m.toInfo(w))
	}
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

func (m *workspaceManager) create(req *proto.Request) *proto.Response {
	if req.RepoPath == "" {
		return proto.Errorf(req.ID, "repoPath required")
	}
	title := strings.TrimSpace(req.Title)
	if title == "" {
		title = "workspace"
	}
	program := req.Program
	if program == "" {
		program = config.LoadConfig().GetProgram()
	}

	// Validate the agent program resolves on PATH *before* creating any worktree
	// or session. A bad/typo'd agent (e.g. a stale "test-program" default) would
	// otherwise leave an orphan worktree plus a session that dies the instant it
	// launches; fail fast with a clear message and create nothing instead.
	if fields := strings.Fields(program); len(fields) == 0 {
		return proto.Errorf(req.ID, "no agent program configured")
	} else if _, err := exec.LookPath(fields[0]); err != nil {
		return proto.Errorf(req.ID, "agent program %q not found on PATH (set a valid agent such as 'copilot' or 'claude'): %v", fields[0], err)
	}

	id := newWorkspaceID()
	sessionName := "ws_" + id
	gitName := slug(title) + "-" + id[:6] // unique branch even for duplicate titles

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
	if err := m.host.startManagedSession(sessionName, program, wt.GetWorktreePath(), cols, rows, req.AutoYes); err != nil {
		// Roll back the worktree so a failed create leaves no orphan.
		_ = wt.Remove()
		_ = wt.Prune()
		return proto.Errorf(req.ID, "start agent: %v", err)
	}

	w := &workspace{
		ID: id, Title: title, Program: program, RepoPath: wt.GetRepoPath(),
		WorktreePath: wt.GetWorktreePath(), Branch: branch, BaseSHA: wt.GetBaseCommitSHA(),
		SessionName: sessionName, AutoYes: req.AutoYes, ExistingBranch: req.BaseBranch != "",
		CreatedUnix: time.Now().Unix(),
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
	delete(m.wss, w.ID)
	m.saveLocked()
	m.mu.Unlock()

	// Kill the agent session, then remove the worktree (keeping the branch so
	// committed/pushed work survives — like Conductor's archive). The agent's
	// ConPTY held the worktree as its cwd; Windows can take a moment to release
	// that directory handle after the process dies, so retry the removal.
	m.host.killSession(w.SessionName)
	wt := w.worktreeFor()
	for i := 0; i < 10; i++ {
		if err := wt.Remove(); err == nil {
			break
		}
		time.Sleep(150 * time.Millisecond)
	}
	_ = wt.Prune()
	m.host.logger.Printf("archived workspace %q (%s)", w.Title, w.ID)
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
