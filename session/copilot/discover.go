package copilot

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"hangar/session/agentcmd"

	cslog "hangar/log"
)

// File-size and per-line limits for untrusted Copilot-owned files (F-30).
const (
	// maxWorkspaceFileBytes is the upper bound for workspace.yaml.
	// Legitimate files are typically < 1 KiB; 1 MiB is extremely generous.
	maxWorkspaceFileBytes = 1 * 1024 * 1024 // 1 MiB

	// maxEventsFileBytes is the upper bound for events.jsonl.
	// 64 MiB allows ~6 000 average-length events; larger files are treated
	// as potential DoS input and skipped gracefully.
	maxEventsFileBytes = 64 * 1024 * 1024 // 64 MiB

	// maxLineBytes caps a single line read from any Copilot-owned file.
	// Prevents a crafted file from forcing a multi-GiB string allocation.
	maxLineBytes = 1 * 1024 * 1024 // 1 MiB
)

// statAndCheckSize stats path and returns (size, nil) when the file exists and
// does not exceed maxBytes.  Returns (0, nil) when the file is absent.
// Returns a non-nil error when the file is a directory or exceeds maxBytes.
func statAndCheckSize(path string, maxBytes int64) (int64, error) {
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	if info.IsDir() {
		return 0, fmt.Errorf("expected file, got directory: %s", path)
	}
	if info.Size() > maxBytes {
		return 0, fmt.Errorf("file %s is too large (%d bytes; max %d)", path, info.Size(), maxBytes)
	}
	return info.Size(), nil
}

const inUseFreshness = 5 * time.Minute

// Discover lists and parses local GitHub Copilot CLI sessions.
func Discover() ([]Session, error) {
	sessions, _, err := discover()
	return sessions, err
}

// DiscoverWithStats is like Discover but also returns the number of sessions that
// were skipped due to per-session parse errors (surfaced as a footer count in the UI).
func DiscoverWithStats() ([]Session, int, error) {
	return discover()
}

func discover() ([]Session, int, error) {
	root := Root()
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, 0, nil
		}
		return nil, 0, fmt.Errorf("read copilot session root: %w", err)
	}

	var dirs []os.DirEntry
	for _, entry := range entries {
		if entry.IsDir() {
			dirs = append(dirs, entry)
		}
	}
	if len(dirs) == 0 {
		return nil, 0, nil
	}

	workerCount := runtime.NumCPU()
	if workerCount < 1 {
		workerCount = 1
	}
	if workerCount > len(dirs) {
		workerCount = len(dirs)
	}

	jobs := make(chan os.DirEntry)
	results := make(chan Session, len(dirs))
	var perSessionErrors atomic.Int64

	var wg sync.WaitGroup
	for i := 0; i < workerCount; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for entry := range jobs {
				session, parseErr := parseSession(root, entry.Name())
				if parseErr != nil {
					perSessionErrors.Add(1)
				}
				results <- session
			}
		}()
	}

	for _, entry := range dirs {
		jobs <- entry
	}
	close(jobs)

	wg.Wait()
	close(results)

	sessions := make([]Session, 0, len(dirs))
	for session := range results {
		sessions = append(sessions, session)
	}

	skipped := int(perSessionErrors.Load())
	if skipped > 0 {
		logWarningf("copilot discovery completed with %d per-session errors", skipped)
	}
	return sessions, skipped, nil
}

func parseSession(root, dirName string) (Session, error) {
	dir := filepath.Join(root, dirName)
	session := Session{Dir: dir}

	// The directory name is attacker-plantable, so only adopt it as the session
	// id if it passes the trust-boundary validator. This keeps a poisoned id
	// (e.g. "a&calc.exe") from ever reaching a launch command line (F-01).
	if agentcmd.ValidSessionID(dirName) {
		session.ID = dirName
	}

	workspace, err := parseWorkspace(filepath.Join(dir, "workspace.yaml"))
	if err != nil {
		logWarningf("failed to parse copilot workspace %s: %v", dir, err)
		return session, err
	}

	if id := workspace["id"]; id != "" {
		// workspace.yaml is untrusted; reject any id that is not UUID-shaped so
		// it can never be concatenated/tokenized into a resume command.
		if !agentcmd.ValidSessionID(id) {
			logWarningf("copilot workspace %s: rejecting invalid session id %q", dir, id)
			return session, fmt.Errorf("invalid copilot session id")
		}
		session.ID = id
	}
	session.Name = workspace["name"]
	session.Repository = workspace["repository"]
	session.OriginRoot = validateOriginRoot(workspace["git_root"])
	session.OriginRef = workspace["branch"]
	session.Cwd = workspace["cwd"]
	session.Branch = workspace["branch"]
	session.CreatedAt = parseWorkspaceTime(workspace["created_at"])
	session.UpdatedAt = parseWorkspaceTime(workspace["updated_at"])

	if hasEvents, scanErr := populateFromEvents(&session); scanErr != nil {
		session.HasEvents = hasEvents
		logWarningf("failed to scan copilot events %s: %v", dir, scanErr)
		err = scanErr
	} else {
		session.HasEvents = hasEvents
	}

	if inUse, inUseErr := sessionInUse(dir, time.Now()); inUseErr != nil {
		logWarningf("failed to inspect copilot lock files %s: %v", dir, inUseErr)
		err = errors.Join(err, inUseErr)
	} else {
		session.InUse = inUse
	}

	return session, err
}

func parseWorkspace(path string) (map[string]string, error) {
	if _, err := statAndCheckSize(path, maxWorkspaceFileBytes); err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	values := make(map[string]string)
	lines := strings.Split(string(data), "\n")
	for lineNum, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("line %d: expected key: value", lineNum+1)
		}

		key := strings.TrimSpace(parts[0])
		if key == "" {
			return nil, fmt.Errorf("line %d: empty key", lineNum+1)
		}
		values[key] = stripOptionalQuotes(strings.TrimSpace(parts[1]))
	}

	return values, nil
}

func stripOptionalQuotes(value string) string {
	if len(value) < 2 {
		return value
	}
	if (value[0] == '"' && value[len(value)-1] == '"') ||
		(value[0] == '\'' && value[len(value)-1] == '\'') {
		return value[1 : len(value)-1]
	}
	return value
}

func parseWorkspaceTime(value string) time.Time {
	if value == "" {
		return time.Time{}
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}
	}
	return parsed
}

func populateFromEvents(session *Session) (bool, error) {
	eventsPath := filepath.Join(session.Dir, "events.jsonl")
	info, err := os.Stat(eventsPath)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	if info.IsDir() || info.Size() == 0 {
		return false, nil
	}

	result, err := scanEvents(eventsPath, true, true)
	if err != nil {
		return true, err
	}

	if result.originRoot != "" {
		if v := validateOriginRoot(result.originRoot); v != "" {
			session.OriginRoot = v
		} else {
			logWarningf("events.jsonl: ignoring suspicious gitRoot %q", result.originRoot)
		}
	}
	session.OriginHead = result.originHead
	if result.originRef != "" {
		session.OriginRef = result.originRef
	}
	if session.Repository == "" && result.repository != "" {
		session.Repository = result.repository
	}
	session.firstUserMsg = result.firstUserMsg

	return true, nil
}

// IsInUse reports whether the session directory currently has a fresh inuse.*.lock.
// It re-checks the lock live, since a Session's discovery-time InUse flag may be
// stale by the time a resume is launched.
func IsInUse(dir string) bool {
	inUse, err := sessionInUse(dir, time.Now())
	if err != nil {
		return false
	}
	return inUse
}

func sessionInUse(dir string, now time.Time) (bool, error) {
	matches, err := filepath.Glob(filepath.Join(dir, "inuse.*.lock"))
	if err != nil {
		return false, err
	}

	for _, match := range matches {
		info, err := os.Stat(match)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return false, err
		}
		// Cross-platform heuristic: PID liveness is platform-specific, so recency is used.
		if !info.IsDir() && info.ModTime().After(now.Add(-inUseFreshness)) {
			return true, nil
		}
	}
	return false, nil
}

type eventsScanResult struct {
	originRoot   string
	originHead   string
	originRef    string
	repository   string
	firstUserMsg string
}

func scanEvents(path string, needOrigin, needUser bool) (eventsScanResult, error) {
	// Reject oversized files before allocating any read buffer (F-30).
	if _, err := statAndCheckSize(path, maxEventsFileBytes); err != nil {
		logWarningf("copilot events rejected: %v", err)
		return eventsScanResult{}, nil // treat oversized file as absent, not a hard error
	}

	file, err := os.Open(path)
	if err != nil {
		return eventsScanResult{}, err
	}
	defer file.Close()

	// Use bufio.Scanner with an explicit per-line cap so a crafted file with a
	// multi-GiB line cannot force a proportional heap allocation (F-30).
	scanner := bufio.NewScanner(file)
	buf := make([]byte, 64*1024) // initial buffer 64 KiB
	scanner.Buffer(buf, maxLineBytes)

	var result eventsScanResult
	foundOrigin := !needOrigin
	foundUser := !needUser

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		scanEventLine(line, &result, &foundOrigin, &foundUser)
		if foundOrigin && foundUser {
			return result, nil
		}
	}
	if err := scanner.Err(); err != nil {
		// bufio.ErrTooLong fires when a single line exceeds maxLineBytes.
		// Log and return whatever we have — a single malformed session must not
		// prevent discovery of all other sessions.
		logWarningf("copilot events scan error (oversized line or I/O) %s: %v", path, err)
		return result, nil
	}
	return result, nil
}

func scanEventLine(line string, result *eventsScanResult, foundOrigin, foundUser *bool) {
	if line == "" {
		return
	}

	var envelope struct {
		Type string          `json:"type"`
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal([]byte(line), &envelope); err != nil {
		return
	}

	switch envelope.Type {
	case "session.start":
		if *foundOrigin {
			return
		}
		var data struct {
			Context struct {
				GitRoot    string `json:"gitRoot"`
				HeadCommit string `json:"headCommit"`
				Branch     string `json:"branch"`
				Repository string `json:"repository"`
			} `json:"context"`
		}
		if err := json.Unmarshal(envelope.Data, &data); err != nil {
			return
		}
		result.originRoot = data.Context.GitRoot
		result.originHead = data.Context.HeadCommit
		result.originRef = data.Context.Branch
		result.repository = data.Context.Repository
		*foundOrigin = true
	case "user.message":
		if *foundUser {
			return
		}
		var data struct {
			Content string `json:"content"`
		}
		if err := json.Unmarshal(envelope.Data, &data); err != nil {
			return
		}
		result.firstUserMsg = data.Content
		*foundUser = true
	}
}

func logWarningf(format string, args ...any) {
	if cslog.WarningLog != nil {
		cslog.WarningLog.Printf(format, args...)
	}
}

// validateOriginRoot vets an OriginRoot/gitRoot value read from untrusted
// Copilot state (workspace.yaml git_root, or events.jsonl context.gitRoot). It
// returns the cleaned path, or "" when the value is structurally unusable
// (empty or relative) or exists on disk but is clearly not a git repository.
//
// A genuine cross-repo path is deliberately preserved here (not home-restricted),
// so developers working under D:\code\ etc. are not broken; the authoritative
// "is this a real local git repo" gate and the cross-repo confirmation are
// enforced at the resume trust boundary (server-side git.IsLocalGitRepo in
// session/winhost, and the TUI's existence check + full-path confirm). A
// not-yet-present absolute path is kept here and re-validated there, so a poisoned
// value can never silently reach `git worktree add` and run a post-checkout
// hook (F-03).
func validateOriginRoot(p string) string {
	p = strings.TrimSpace(p)
	if p == "" || !filepath.IsAbs(p) {
		return ""
	}
	clean := filepath.Clean(p)
	if info, err := os.Stat(clean); err == nil {
		// The path exists: require it to look like a git repository root (a .git
		// entry — a directory for a clone, or a file for a linked worktree).
		// Something that exists but is not a repo is treated as poisoned.
		if !info.IsDir() {
			return ""
		}
		if _, gErr := os.Stat(filepath.Join(clean, ".git")); gErr != nil {
			return ""
		}
	}
	return clean
}