package copilot

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestIndexSearchMetadataAndContent(t *testing.T) {
	ix := newTestIndex(t)
	sessions := []Session{
		indexSession(t, "meta", "Planning Browser", "owner/repo", "main", "", timeAt(t, "2026-06-16T10:00:00Z"), nil),
		indexSession(t, "body", "Other", "owner/repo", "dev", "", timeAt(t, "2026-06-16T11:00:00Z"), []string{"rarebodytoken from user"}),
	}
	require.NoError(t, ix.Build(context.Background(), sessions))

	requireIDs(t, ix.Search(context.Background(), sessions, "browser"), "meta")
	requireIDs(t, ix.Search(context.Background(), sessions, "rarebodytoken"), "body")
	requireIDs(t, ix.Search(context.Background(), sessions, "RAREBODYTOKEN user"), "body")
	require.Empty(t, ix.Search(context.Background(), sessions, "rarebodytoken missing"))
}

func TestIndexSearchBeforeBuildUsesMetadataFallback(t *testing.T) {
	ix := newTestIndex(t)
	sessions := []Session{{ID: "fallback", Name: "Fallback Name", Repository: "owner/repo", Branch: "main", OriginRef: "origin-ref"}}

	requireIDs(t, ix.Search(context.Background(), sessions, "fallback origin-ref"), "fallback")
}

func TestIndexRanking(t *testing.T) {
	ix := newTestIndex(t)
	oldTime := timeAt(t, "2026-06-16T10:00:00Z")
	newTime := timeAt(t, "2026-06-16T12:00:00Z")
	sessions := []Session{
		indexSession(t, "old", "Old", "owner/repo", "main", "", oldTime, []string{"alpha alpha alpha"}),
		indexSession(t, "new", "New", "owner/repo", "main", "", newTime, []string{"alpha"}),
		indexSession(t, "tie-a", "Tie A", "owner/repo", "main", "", oldTime, []string{"beta"}),
		indexSession(t, "tie-b", "Tie B", "owner/repo", "main", "", oldTime, []string{"beta"}),
	}
	require.NoError(t, ix.Build(context.Background(), sessions))

	requireIDs(t, ix.Search(context.Background(), sessions, ""), "new", "old", "tie-a", "tie-b")
	requireIDs(t, ix.Search(context.Background(), sessions, "alpha"), "old", "new")
	requireIDs(t, ix.Search(context.Background(), sessions, "beta"), "tie-a", "tie-b")
}

func TestIndexInvalidationReindexesChangedAndStaleEntries(t *testing.T) {
	path := filepath.Join(t.TempDir(), "copilot-index.json")
	ix, err := openIndexAt(path)
	require.NoError(t, err)
	sessions := []Session{
		indexSession(t, "changed", "Changed", "owner/repo", "main", "", timeAt(t, "2026-06-16T10:00:00Z"), []string{"oldterm"}),
		indexSession(t, "stable", "Stable", "owner/repo", "main", "", timeAt(t, "2026-06-16T11:00:00Z"), []string{"stableterm"}),
	}
	require.NoError(t, ix.Build(context.Background(), sessions))
	requireIDs(t, ix.Search(context.Background(), sessions, "oldterm"), "changed")

	appendEvent(t, sessions[0].Dir, "assistant.message", "newterm")
	bumpEventsMTime(t, sessions[0].Dir)
	require.NoError(t, ix.Build(context.Background(), sessions))
	requireIDs(t, ix.Search(context.Background(), sessions, "newterm"), "changed")
	requireIDs(t, ix.Search(context.Background(), sessions, "stableterm"), "stable")

	// A per-entry schema mismatch is stale even if size and mtime match.
	ix.mu.Lock()
	stale := ix.entries["stable"]
	stale.SchemaVer = indexSchemaVersion + 1
	stale.Haystack = "corrupted stale haystack"
	ix.entries["stable"] = stale
	ix.mu.Unlock()
	require.NoError(t, ix.persist(context.Background(), ix.entries))

	reopened, err := openIndexAt(path)
	require.NoError(t, err)
	require.NoError(t, reopened.Build(context.Background(), sessions))
	requireIDs(t, reopened.Search(context.Background(), sessions, "stableterm"), "stable")
	require.Empty(t, reopened.Search(context.Background(), sessions, "corrupted"))
}

func TestIndexDropsMissingSessionsAndPersistenceRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "copilot-index.json")
	ix, err := openIndexAt(path)
	require.NoError(t, err)
	sessions := []Session{
		indexSession(t, "keep", "Keep", "owner/repo", "main", "", timeAt(t, "2026-06-16T10:00:00Z"), []string{"persisttoken"}),
		indexSession(t, "drop", "Drop", "owner/repo", "main", "", timeAt(t, "2026-06-16T11:00:00Z"), []string{"droptoken"}),
	}
	require.NoError(t, ix.Build(context.Background(), sessions))

	reopened, err := openIndexAt(path)
	require.NoError(t, err)
	requireIDs(t, reopened.Search(context.Background(), sessions, "persisttoken"), "keep")

	require.NoError(t, reopened.Build(context.Background(), sessions[:1]))
	reopenedAgain, err := openIndexAt(path)
	require.NoError(t, err)
	require.Empty(t, reopenedAgain.Search(context.Background(), sessions, "droptoken"))
}

func TestIndexCap(t *testing.T) {
	ix := newTestIndex(t)
	inside := "insidecaptoken"
	outside := "beyondcaptoken"
	content := inside + " " + strings.Repeat("a", indexCapBytes+512) + outside
	sessions := []Session{indexSession(t, "cap", "Cap", "owner/repo", "main", "", timeAt(t, "2026-06-16T10:00:00Z"), []string{content})}
	require.NoError(t, ix.Build(context.Background(), sessions))

	requireIDs(t, ix.Search(context.Background(), sessions, inside), "cap")
	require.Empty(t, ix.Search(context.Background(), sessions, outside))
}

func TestIndexCancellation(t *testing.T) {
	ix := newTestIndex(t)
	sessions := []Session{indexSession(t, "cancel", "Cancel", "owner/repo", "main", "", timeAt(t, "2026-06-16T10:00:00Z"), []string{"token"})}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	require.ErrorIs(t, ix.Build(ctx, sessions), context.Canceled)
	require.Nil(t, ix.Search(ctx, sessions, "token"))
}

func TestOpenIndexCorruptOrFileSchemaMismatchStartsEmpty(t *testing.T) {
	path := filepath.Join(t.TempDir(), "copilot-index.json")
	require.NoError(t, os.WriteFile(path, []byte("not-json"), 0o644))
	ix, err := openIndexAt(path)
	require.NoError(t, err)
	require.Empty(t, ix.entries)

	data, err := json.Marshal(indexFile{SchemaVer: indexSchemaVersion + 1, Entries: []indexEntry{{ID: "stale"}}})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, data, 0o644))
	ix, err = openIndexAt(path)
	require.NoError(t, err)
	require.Empty(t, ix.entries)
}

func newTestIndex(t *testing.T) *Index {
	t.Helper()
	ix, err := openIndexAt(filepath.Join(t.TempDir(), "copilot-index.json"))
	require.NoError(t, err)
	return ix
}

func indexSession(t *testing.T, id, name, repo, branch, originRef string, updatedAt time.Time, contents []string) Session {
	t.Helper()
	dir := filepath.Join(t.TempDir(), id)
	require.NoError(t, os.MkdirAll(dir, 0o755))
	if contents != nil {
		for i, content := range contents {
			typ := "user.message"
			if i%2 == 1 {
				typ = "assistant.message"
			}
			appendEvent(t, dir, typ, content)
		}
	}
	if originRef == "" {
		originRef = branch
	}
	return Session{ID: id, Dir: dir, Name: name, Repository: repo, Branch: branch, OriginRef: originRef, UpdatedAt: updatedAt}
}

func appendEvent(t *testing.T, dir, typ, content string) {
	t.Helper()
	line, err := json.Marshal(map[string]any{"type": typ, "data": map[string]any{"content": content}})
	require.NoError(t, err)
	file, err := os.OpenFile(filepath.Join(dir, "events.jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	require.NoError(t, err)
	_, err = file.Write(append(line, '\n'))
	require.NoError(t, err)
	require.NoError(t, file.Close())
}

func bumpEventsMTime(t *testing.T, dir string) {
	t.Helper()
	newTime := time.Now().Add(2 * time.Second).Truncate(time.Second)
	require.NoError(t, os.Chtimes(filepath.Join(dir, "events.jsonl"), newTime, newTime))
}

func requireIDs(t *testing.T, sessions []Session, want ...string) {
	t.Helper()
	got := make([]string, len(sessions))
	for i, session := range sessions {
		got[i] = session.ID
	}
	require.Equal(t, want, got)
}

func timeAt(t *testing.T, value string) time.Time {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339, value)
	require.NoError(t, err)
	return parsed
}
