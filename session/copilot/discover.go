package copilot

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	cslog "hangar/log"
)

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
	session := Session{ID: dirName, Dir: dir}

	workspace, err := parseWorkspace(filepath.Join(dir, "workspace.yaml"))
	if err != nil {
		logWarningf("failed to parse copilot workspace %s: %v", dir, err)
		return session, err
	}

	if id := workspace["id"]; id != "" {
		session.ID = id
	}
	session.Name = workspace["name"]
	session.Repository = workspace["repository"]
	session.OriginRoot = workspace["git_root"]
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
		session.OriginRoot = result.originRoot
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
	file, err := os.Open(path)
	if err != nil {
		return eventsScanResult{}, err
	}
	defer file.Close()

	reader := bufio.NewReader(file)
	var result eventsScanResult
	foundOrigin := !needOrigin
	foundUser := !needUser

	for {
		line, readErr := reader.ReadString('\n')
		if line != "" {
			scanEventLine(strings.TrimSpace(line), &result, &foundOrigin, &foundUser)
			if foundOrigin && foundUser {
				return result, nil
			}
		}

		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				return result, nil
			}
			return result, readErr
		}
	}
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
