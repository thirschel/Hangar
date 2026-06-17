package copilot

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"claude-squad/config"
	cslog "claude-squad/log"
)

const indexSchemaVersion = 1
const indexCapBytes = 32 * 1024

type indexEntry struct {
	ID         string    `json:"id"`
	Size       int64     `json:"size"`
	ModTime    time.Time `json:"mod_time"`
	SchemaVer  int       `json:"schema_ver"`
	OriginRoot string    `json:"origin_root"`
	OriginHead string    `json:"origin_head"`
	OriginRef  string    `json:"origin_ref"`
	Haystack   string    `json:"haystack"`
}

type Index struct {
	path    string
	entries map[string]indexEntry
	mu      sync.Mutex
}

type indexFile struct {
	SchemaVer int          `json:"schema_ver"`
	Entries   []indexEntry `json:"entries"`
}

// IndexPath returns the on-disk Copilot search index path.
func IndexPath() (string, error) {
	dir, err := config.GetConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "copilot-index.json"), nil
}

// OpenIndex loads the production Copilot search index, or an empty index when absent or corrupt.
func OpenIndex() (*Index, error) {
	path, err := IndexPath()
	if err != nil {
		return nil, err
	}
	return openIndexAt(path)
}

func openIndexAt(path string) (*Index, error) {
	ix := &Index{path: path, entries: make(map[string]indexEntry)}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return ix, nil
		}
		return nil, err
	}

	var stored indexFile
	if err := json.Unmarshal(data, &stored); err != nil || stored.SchemaVer != indexSchemaVersion {
		return ix, nil
	}
	for _, entry := range stored.Entries {
		if entry.ID != "" {
			ix.entries[entry.ID] = entry
		}
	}
	return ix, nil
}

// Build refreshes the content index for sessions and persists it to disk.
func (ix *Index) Build(ctx context.Context, sessions []Session) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	ix.mu.Lock()
	existing := make(map[string]indexEntry, len(ix.entries))
	for id, entry := range ix.entries {
		existing[id] = entry
	}
	ix.mu.Unlock()

	refreshed := make(map[string]indexEntry, len(sessions))
	for _, session := range sessions {
		if err := ctx.Err(); err != nil {
			return err
		}

		entry, err := ix.buildEntry(ctx, session, existing[session.ID])
		if err != nil {
			return err
		}
		refreshed[session.ID] = entry
	}

	if err := ix.persist(ctx, refreshed); err != nil {
		return err
	}

	ix.mu.Lock()
	ix.entries = refreshed
	ix.mu.Unlock()
	return nil
}

// Search returns sessions matching query, ranked by relevance and recency.
func (ix *Index) Search(ctx context.Context, sessions []Session, query string) []Session {
	if ctx.Err() != nil {
		return nil
	}
	terms := strings.Fields(strings.ToLower(query))

	ix.mu.Lock()
	entries := make(map[string]indexEntry, len(ix.entries))
	for id, entry := range ix.entries {
		entries[id] = entry
	}
	ix.mu.Unlock()

	type result struct {
		session Session
		hits    int
	}
	results := make([]result, 0, len(sessions))
	for _, session := range sessions {
		if ctx.Err() != nil {
			return nil
		}
		if len(terms) == 0 {
			results = append(results, result{session: session})
			continue
		}

		entry, hasEntry := entries[session.ID]
		haystack := searchHaystack(session, entry, hasEntry)
		hits := 0
		matched := true
		for _, term := range terms {
			count := strings.Count(haystack, term)
			if count == 0 {
				matched = false
				break
			}
			hits += count
		}
		if matched {
			results = append(results, result{session: session, hits: hits})
		}
	}

	sort.SliceStable(results, func(i, j int) bool {
		if results[i].hits != results[j].hits {
			return results[i].hits > results[j].hits
		}
		if !results[i].session.UpdatedAt.Equal(results[j].session.UpdatedAt) {
			return results[i].session.UpdatedAt.After(results[j].session.UpdatedAt)
		}
		return results[i].session.ID < results[j].session.ID
	})

	out := make([]Session, len(results))
	for i, result := range results {
		out[i] = result.session
	}
	return out
}

func (ix *Index) buildEntry(ctx context.Context, session Session, existing indexEntry) (indexEntry, error) {
	eventsPath := filepath.Join(session.Dir, "events.jsonl")
	size, modTime, err := eventsStat(eventsPath)
	if err != nil {
		return indexEntry{}, err
	}

	if existing.ID == session.ID && existing.SchemaVer == indexSchemaVersion && existing.Size == size && existing.ModTime.Equal(modTime) {
		return existing, nil
	}

	content := ""
	if size > 0 {
		var scanErr error
		content, scanErr = scanIndexContent(ctx, eventsPath)
		if scanErr != nil {
			return indexEntry{}, scanErr
		}
	}

	return indexEntry{
		ID:         session.ID,
		Size:       size,
		ModTime:    modTime,
		SchemaVer:  indexSchemaVersion,
		OriginRoot: session.OriginRoot,
		OriginHead: session.OriginHead,
		OriginRef:  session.OriginRef,
		Haystack:   strings.ToLower(strings.Join([]string{session.Name, session.Repository, session.Branch, session.OriginRef, content}, " ")),
	}, nil
}

func eventsStat(path string) (int64, time.Time, error) {
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, time.Time{}, nil
		}
		return 0, time.Time{}, err
	}
	if info.IsDir() {
		return 0, time.Time{}, nil
	}
	return info.Size(), info.ModTime(), nil
}

func scanIndexContent(ctx context.Context, path string) (string, error) {
	// Reject oversized files before allocating any read buffer (F-30).
	if _, err := statAndCheckSize(path, maxEventsFileBytes); err != nil {
		logWarningf("copilot index scan rejected: %v", err)
		return "", nil // treat oversized file as empty haystack
	}

	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	defer file.Close()

	// Use bufio.Scanner with an explicit per-line cap (F-30).
	scanner := bufio.NewScanner(file)
	buf := make([]byte, 64*1024) // initial buffer 64 KiB
	scanner.Buffer(buf, maxLineBytes)

	var b strings.Builder
	for b.Len() < indexCapBytes {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		if !scanner.Scan() {
			break
		}
		appendIndexContent(&b, strings.TrimSpace(scanner.Text()))
	}
	if err := scanner.Err(); err != nil {
		// bufio.ErrTooLong: partial haystack is acceptable.
		logWarningf("copilot index scan error (oversized line or I/O) %s: %v", path, err)
	}
	return b.String(), nil
}

func appendIndexContent(b *strings.Builder, line string) {
	if line == "" || b.Len() >= indexCapBytes {
		return
	}

	var envelope struct {
		Type string          `json:"type"`
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal([]byte(line), &envelope); err != nil {
		return
	}
	if envelope.Type != "user.message" && envelope.Type != "assistant.message" {
		return
	}
	var data struct {
		Content string `json:"content"`
	}
	if err := json.Unmarshal(envelope.Data, &data); err != nil || data.Content == "" {
		return
	}

	if b.Len() > 0 {
		b.WriteByte(' ')
	}
	remaining := indexCapBytes - b.Len()
	if len(data.Content) > remaining {
		b.WriteString(data.Content[:remaining])
		return
	}
	b.WriteString(data.Content)
}

func searchHaystack(session Session, entry indexEntry, hasEntry bool) string {
	if hasEntry && entry.ID == session.ID {
		return entry.Haystack
	}
	return strings.ToLower(strings.Join([]string{session.DisplayName(), session.Repository, session.Branch, session.OriginRef}, " "))
}

func (ix *Index) persist(ctx context.Context, entries map[string]indexEntry) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(ix.path), 0o700); err != nil {
		return err
	}

	unlock := ix.tryLock()
	defer unlock()

	stored := indexFile{SchemaVer: indexSchemaVersion, Entries: make([]indexEntry, 0, len(entries))}
	for _, entry := range entries {
		stored.Entries = append(stored.Entries, entry)
	}
	sort.Slice(stored.Entries, func(i, j int) bool { return stored.Entries[i].ID < stored.Entries[j].ID })

	data, err := json.MarshalIndent(stored, "", "  ")
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	tmp, err := os.CreateTemp(filepath.Dir(ix.path), filepath.Base(ix.path)+"-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, ix.path)
}

func (ix *Index) tryLock() func() {
	lockPath := ix.path + ".lock"
	file, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		if cslog.WarningLog != nil {
			cslog.WarningLog.Printf("copilot index lock unavailable %s: %v", lockPath, err)
		}
		return func() {}
	}
	_, _ = fmt.Fprintf(file, "%d\n", os.Getpid())
	return func() {
		_ = file.Close()
		_ = os.Remove(lockPath)
	}
}
