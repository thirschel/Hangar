package copilot

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestDiscoverWellFormedFixture(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CS_COPILOT_SESSION_DIR", root)

	writeSession(t, root, "2331de89-df3c-43cf-8ac0-2c8f0886b9a4", `id: 2331de89-df3c-43cf-8ac0-2c8f0886b9a4
cwd: D:\dev\claude-squad
git_root: D:\dev\claude-squad
repository: thirschel/claude-squad
host_type: github
branch: desktop-core-daemon
name: Plan Session Browser Feature
user_named: false
created_at: 2026-06-16T21:15:55.071Z
updated_at: 2026-06-16T21:25:56.277Z
`, []string{`{"type":"user.message","data":{"content":"Build the browser","timestamp":"2026-06-16T21:16:00Z"}}`})

	sessions, err := Discover()
	require.NoError(t, err)
	require.Len(t, sessions, 1)

	session := sessions[0]
	require.Equal(t, "2331de89-df3c-43cf-8ac0-2c8f0886b9a4", session.ID)
	require.Equal(t, "Plan Session Browser Feature", session.Name)
	require.Equal(t, "thirschel/claude-squad", session.Repository)
	require.Equal(t, "desktop-core-daemon", session.Branch)
	require.Equal(t, parseTime(t, "2026-06-16T21:15:55.071Z"), session.CreatedAt)
	require.Equal(t, parseTime(t, "2026-06-16T21:25:56.277Z"), session.UpdatedAt)
	require.True(t, session.HasEvents)
}

func TestDiscoverOriginFreezeUsesSessionStart(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CS_COPILOT_SESSION_DIR", root)

	writeSession(t, root, "origin-freeze", `id: origin-freeze
cwd: D:\rewritten\cwd
git_root: D:\rewritten\root
repository: thirschel/claude-squad
branch: rewritten-branch
`, []string{
		`{"type":"session.start","data":{"context":{"gitRoot":"D:\\original\\root","headCommit":"abc123","branch":"original-branch","repository":"thirschel/claude-squad"}}}`,
		`{"type":"user.message","data":{"content":"hello"}}`,
	})

	session := requireSession(t, root, "origin-freeze")
	require.Equal(t, `D:\original\root`, session.OriginRoot)
	require.Equal(t, "abc123", session.OriginHead)
	require.Equal(t, "original-branch", session.OriginRef)
	require.Equal(t, `D:\rewritten\cwd`, session.Cwd)
	require.Equal(t, "rewritten-branch", session.Branch)
}

func TestDiscoverOriginRootDoesNotFallBackToDeletedCwd(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CS_COPILOT_SESSION_DIR", root)

	writeSession(t, root, "empty-origin", `id: empty-origin
cwd: D:\deleted\cwd
git_root:
repository: thirschel/claude-squad
branch: yaml-branch
`, nil)

	session := requireSession(t, root, "empty-origin")
	require.Empty(t, session.OriginRoot, "deleted cwd is informational and must not be used as an origin fallback")
	require.Equal(t, "yaml-branch", session.OriginRef)
	require.Equal(t, `D:\deleted\cwd`, session.Cwd)
}

func TestDisplayNameFallbackTiers(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CS_COPILOT_SESSION_DIR", root)

	writeSession(t, root, "user-msg", `id: user-msg
repository: owner/repo
branch: main
`, []string{`{"type":"user.message","data":{"content":"  first\nmessage from user  "}}`})
	writeSession(t, root, "repo-branch", `id: repo-branch
repository: owner/repo
branch: main
`, nil)
	writeSession(t, root, "abcdefgh12345678", `id: abcdefgh12345678
`, nil)

	sessions, err := Discover()
	require.NoError(t, err)
	byID := sessionsByID(sessions)

	require.Equal(t, "first message from user", byID["user-msg"].DisplayName())
	require.Equal(t, "owner/repo@main", byID["repo-branch"].DisplayName())
	require.Equal(t, "abcdefgh", byID["abcdefgh12345678"].DisplayName())
}

func TestDiscoverMissingAndEmptyEventsAreNotHasEvents(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CS_COPILOT_SESSION_DIR", root)

	writeSession(t, root, "missing-events", `id: missing-events
`, nil)
	dir := writeSession(t, root, "empty-events", `id: empty-events
`, nil)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "events.jsonl"), nil, 0o644))

	sessions, err := Discover()
	require.NoError(t, err)
	byID := sessionsByID(sessions)

	require.False(t, byID["missing-events"].HasEvents)
	require.False(t, byID["empty-events"].HasEvents)
}

func TestDiscoverMalformedWorkspaceIsMinimalAndDoesNotAffectOthers(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CS_COPILOT_SESSION_DIR", root)

	badDir := writeSession(t, root, "bad-workspace", "not yaml at all\n", []string{`{"type":"user.message","data":{"content":"ignored"}}`})
	writeSession(t, root, "good-workspace", `id: good-workspace
name: Good
`, nil)

	sessions, err := Discover()
	require.NoError(t, err)
	byID := sessionsByID(sessions)

	require.Equal(t, Session{ID: "bad-workspace", Dir: badDir}, byID["bad-workspace"])
	require.Equal(t, "Good", byID["good-workspace"].Name)
}

func TestDiscoverInUseFreshVsStaleLocks(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CS_COPILOT_SESSION_DIR", root)

	freshDir := writeSession(t, root, "fresh", "id: fresh\n", nil)
	staleDir := writeSession(t, root, "stale", "id: stale\n", nil)

	require.NoError(t, os.WriteFile(filepath.Join(freshDir, "inuse.123.lock"), []byte(""), 0o644))
	staleLock := filepath.Join(staleDir, "inuse.456.lock")
	require.NoError(t, os.WriteFile(staleLock, []byte(""), 0o644))
	staleTime := time.Now().Add(-inUseFreshness - time.Minute)
	require.NoError(t, os.Chtimes(staleLock, staleTime, staleTime))

	sessions, err := Discover()
	require.NoError(t, err)
	byID := sessionsByID(sessions)

	require.True(t, byID["fresh"].InUse)
	require.False(t, byID["stale"].InUse)
}

func TestRootHonorsOverride(t *testing.T) {
	t.Setenv("CS_COPILOT_SESSION_DIR", `D:\custom\copilot\sessions`)
	require.Equal(t, `D:\custom\copilot\sessions`, Root())
}

func TestDiscoverMissingRootReturnsNil(t *testing.T) {
	root := filepath.Join(t.TempDir(), "missing")
	t.Setenv("CS_COPILOT_SESSION_DIR", root)

	sessions, err := Discover()
	require.NoError(t, err)
	require.Nil(t, sessions)
}

func TestFirstUserMessageStreamsFromDisk(t *testing.T) {
	root := t.TempDir()
	dir := writeSession(t, root, "first-user", "id: first-user\n", []string{
		`{not json`,
		`{"type":"assistant.message","data":{"content":"assistant"}}`,
		`{"type":"user.message","data":{"content":"first user"}}`,
		`{"type":"user.message","data":{"content":"second user"}}`,
	})

	msg, err := FirstUserMessage(Session{ID: "first-user", Dir: dir})
	require.NoError(t, err)
	require.Equal(t, "first user", msg)
}

func writeSession(t *testing.T, root, id, workspace string, events []string) string {
	t.Helper()

	dir := filepath.Join(root, id)
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "workspace.yaml"), []byte(workspace), 0o644))
	if events != nil {
		require.NoError(t, os.WriteFile(filepath.Join(dir, "events.jsonl"), []byte(joinJSONLines(events)), 0o644))
	}
	return dir
}

func joinJSONLines(lines []string) string {
	if len(lines) == 0 {
		return ""
	}
	return strings.Join(lines, "\n") + "\n"
}

func TestDiscoverIsReadOnly(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CS_COPILOT_SESSION_DIR", root)

	writeSession(t, root, "ro-1", "id: ro-1\nname: One\nrepository: a/b\n",
		[]string{`{"type":"user.message","data":{"content":"hi"}}`})
	writeSession(t, root, "ro-2", "id: ro-2\nname: Two\n", nil)

	before := snapshotTree(t, root)
	_, _, err := DiscoverWithStats()
	require.NoError(t, err)
	after := snapshotTree(t, root)

	require.Equal(t, before, after, "Discover must not create, delete, or modify any files under the session-state dir")
}

func TestDiscoverWithStatsCountsSkipped(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CS_COPILOT_SESSION_DIR", root)

	writeSession(t, root, "good", "id: good\nname: Good\n", nil)
	// A malformed workspace.yaml (a line with no key: value) yields a minimal Session
	// but is counted as skipped.
	bad := filepath.Join(root, "bad")
	require.NoError(t, os.MkdirAll(bad, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(bad, "workspace.yaml"), []byte("this is not valid yaml at all"), 0o644))

	sessions, skipped, err := DiscoverWithStats()
	require.NoError(t, err)
	require.Len(t, sessions, 2, "both sessions are still discovered")
	require.Equal(t, 1, skipped, "the malformed session is counted as skipped")
}

func snapshotTree(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	require.NoError(t, filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		out[rel] = fmt.Sprintf("%d:%d", info.Size(), info.ModTime().UnixNano())
		return nil
	}))
	return out
}

func requireSession(t *testing.T, root, id string) Session {
	t.Helper()

	sessions, err := Discover()
	require.NoError(t, err)
	byID := sessionsByID(sessions)
	session, ok := byID[id]
	require.True(t, ok, "session %q not found in %s", id, root)
	return session
}

func sessionsByID(sessions []Session) map[string]Session {
	byID := make(map[string]Session, len(sessions))
	for _, session := range sessions {
		byID[session.ID] = session
	}
	return byID
}

func parseTime(t *testing.T, value string) time.Time {
	t.Helper()

	parsed, err := time.Parse(time.RFC3339, value)
	require.NoError(t, err)
	return parsed
}
