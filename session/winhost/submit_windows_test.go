//go:build windows

package winhost

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestResolveSubmitMethod(t *testing.T) {
	cases := []struct {
		name      string
		bracketed bool
		override  string
		want      string
	}{
		{"auto bracketed -> paste", true, "", submitPasteMode},
		{"auto plain -> chunk", false, "", submitChunk},
		{"override paste wins over plain", false, "paste", submitPasteMode},
		{"override chunk wins over bracketed", true, "chunk", submitChunk},
		{"override burst", true, "burst", submitBurst},
		{"override case/space-insensitive", false, "  Paste ", submitPasteMode},
		{"unknown override falls back to auto", true, "weird", submitPasteMode},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := resolveSubmitMethod(tc.bracketed, tc.override); got != tc.want {
				t.Fatalf("resolveSubmitMethod(%v,%q)=%q want %q", tc.bracketed, tc.override, got, tc.want)
			}
		})
	}
}

func TestChunkString(t *testing.T) {
	if got := chunkString("hello", 0); len(got) != 1 || got[0] != "hello" {
		t.Fatalf("n<=0 should not split: %q", got)
	}
	if got := chunkString("short", 10); len(got) != 1 {
		t.Fatalf("short string should be one chunk: %q", got)
	}
	got := chunkString("abcdefg", 3)
	want := []string{"abc", "def", "g"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("chunkString=%q want %q", got, want)
	}
	// Multi-byte runes must never be split mid-rune.
	multi := chunkString("héllo wörld", 4)
	if strings.Join(multi, "") != "héllo wörld" {
		t.Fatalf("rejoined chunks lost data: %q", multi)
	}
	for _, c := range multi {
		if !strings.ContainsRune("héllo wörld", []rune(c)[0]) {
			t.Fatalf("chunk %q has a broken leading rune", c)
		}
	}
}

func TestSubmitWritesFramingAndNoCR(t *testing.T) {
	const text = "hello agent please do the thing"

	paste := submitWrites(text, submitPasteMode, promptChunkRunes)
	if len(paste) != 1 {
		t.Fatalf("paste should be a single write, got %d", len(paste))
	}
	if !strings.HasPrefix(string(paste[0]), bracketedPasteStart) || !strings.HasSuffix(string(paste[0]), bracketedPasteEnd) {
		t.Fatalf("paste write not framed in bracketed markers: %q", paste[0])
	}

	burst := submitWrites(text, submitBurst, promptChunkRunes)
	if len(burst) != 1 || string(burst[0]) != text {
		t.Fatalf("burst should be the raw text in one write, got %q", burst)
	}

	long := strings.Repeat("a", 200)
	chunks := submitWrites(long, submitChunk, promptChunkRunes)
	if len(chunks) < 2 {
		t.Fatalf("a 200-char prompt should chunk into >=2 writes, got %d", len(chunks))
	}
	var joined strings.Builder
	for _, c := range chunks {
		if strings.Contains(string(c), bracketedPasteStart) || strings.Contains(string(c), bracketedPasteEnd) {
			t.Fatalf("chunk must not contain paste markers: %q", c)
		}
		joined.Write(c)
	}
	if joined.String() != long {
		t.Fatalf("rejoined chunks != original")
	}

	// No injection method may emit a CR — the caller submits separately.
	for _, m := range []string{submitPasteMode, submitChunk, submitBurst} {
		for _, w := range submitWrites(text, m, promptChunkRunes) {
			if bytes.IndexByte(w, '\r') >= 0 {
				t.Fatalf("method %q emitted a CR in its typing writes: %q", m, w)
			}
		}
	}
}

func TestTailRows(t *testing.T) {
	screen := "top line\n\nmiddle   \n\n\nINPUT > the prompt text\n   \n"
	got := tailRows(screen, 2)
	if !strings.Contains(got, "INPUT > the prompt text") {
		t.Fatalf("tailRows dropped the input row: %q", got)
	}
	if strings.Contains(got, "top line") {
		t.Fatalf("tailRows should only keep the last 2 non-blank rows: %q", got)
	}
	if got := tailRows("only one", 6); got != "only one" {
		t.Fatalf("single row tail = %q", got)
	}
}

func TestChooseEnterBytes(t *testing.T) {
	cr := []byte{'\r'}
	cases := []struct {
		name     string
		override string
		want     []byte
	}{
		{"default -> CR", "", cr},
		{"override cr", "cr", cr},
		{"override lf", "lf", []byte{'\n'}},
		{"override crlf", "crlf", []byte{'\r', '\n'}},
		{"unknown -> CR", "weird", cr},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := chooseEnterBytes(tc.override); !bytes.Equal(got, tc.want) {
				t.Fatalf("chooseEnterBytes(%q)=%q want %q", tc.override, got, tc.want)
			}
		})
	}
}

// TestSubmitPromptSendsFocusInThenEnter verifies the daemon sends a terminal
// focus-in (ESC[I) before the submit Enter — the fix for focus-reporting CLIs
// (copilot) that accept typed text but refuse to submit while they believe the
// terminal is unfocused — and that a plain CR is what submits (the working manual
// Enter is a bare CR).
func TestSubmitPromptSendsFocusInThenEnter(t *testing.T) {
	home := testHome(t)
	_, h, cleanup := startTestHostWithHandle(t)
	defer cleanup()
	w := injectWorkspace(t, h, "submit-focus", "cpa", filepath.Join(home, "wt"))
	h.mu.RLock()
	f := h.sessions[w.SessionName].(*fakeSession)
	h.mu.RUnlock()
	f.mu.Lock()
	f.writes = nil
	f.mu.Unlock()
	// The agent only submits a CR that arrives AFTER a focus-in.
	focused := false
	f.sendHook = func(fs *fakeSession, b []byte) error {
		if string(b) == focusInSeq {
			focused = true
		}
		if focused && len(b) == 1 && b[0] == '\r' {
			fs.mu.Lock()
			fs.busy = true
			fs.lastOutputMs = time.Now().UnixMilli()
			fs.mu.Unlock()
		}
		return nil
	}

	_, submitted := h.workspaces.submitPrompt(w.ID, "continue the work")
	if !submitted {
		t.Fatalf("focused submit not detected as submitted")
	}
	f.mu.Lock()
	writes := f.writes
	f.mu.Unlock()
	firstFocus, firstCR := -1, -1
	for i, wr := range writes {
		if firstFocus < 0 && string(wr) == focusInSeq {
			firstFocus = i
		}
		if firstCR < 0 && len(wr) == 1 && wr[0] == '\r' {
			firstCR = i
		}
	}
	if firstFocus < 0 {
		t.Fatalf("expected a focus-in (ESC[I) write")
	}
	if firstCR < 0 {
		t.Fatalf("expected a bare CR submit write")
	}
	if firstFocus > firstCR {
		t.Fatalf("focus-in must precede the submit CR (focus@%d cr@%d)", firstFocus, firstCR)
	}
}

func TestSubmitPromptBracketedPasteSubmits(t *testing.T) {
	home := testHome(t)
	_, h, cleanup := startTestHostWithHandle(t)
	defer cleanup()
	t.Setenv("HANGAR_SUBMIT_FOCUS", "0") // isolate the paste/CR framing from focus-in writes
	w := injectWorkspace(t, h, "submit-paste", "claude", filepath.Join(home, "wt"))
	h.mu.RLock()
	f := h.sessions[w.SessionName].(*fakeSession)
	h.mu.RUnlock()
	// The agent "accepts" the prompt: a standalone CR makes it go busy.
	f.sendHook = func(fs *fakeSession, b []byte) error {
		if len(b) == 1 && b[0] == '\r' {
			fs.mu.Lock()
			fs.busy = true
			fs.lastOutputMs = time.Now().UnixMilli()
			fs.mu.Unlock()
		}
		return nil
	}

	method, submitted := h.workspaces.submitPrompt(w.ID, "please continue the work")
	if method != submitPasteMode {
		t.Fatalf("method = %q, want paste", method)
	}
	if !submitted {
		t.Fatalf("bracketed submit was not detected as submitted")
	}

	f.mu.Lock()
	writes := f.writes
	f.mu.Unlock()
	if len(writes) < 2 {
		t.Fatalf("expected at least a paste write + a CR, got %d", len(writes))
	}
	if !strings.HasPrefix(string(writes[0]), bracketedPasteStart) || !strings.HasSuffix(string(writes[0]), bracketedPasteEnd) {
		t.Fatalf("first write not a bracketed paste: %q", writes[0])
	}
	sawCR := false
	for _, wr := range writes {
		if len(wr) == 1 && wr[0] == '\r' {
			sawCR = true
		}
		if strings.Contains(string(wr), bracketedPasteEnd) && bytes.IndexByte(wr, '\r') >= 0 {
			t.Fatalf("CR must not share a write with the paste body: %q", wr)
		}
	}
	if !sawCR {
		t.Fatalf("no standalone CR was sent")
	}
}

// TestSubmitPromptChunkFallback verifies a non-bracketed agent is typed into in
// chunks (no paste markers) with a separate CR.
func TestSubmitPromptChunkFallback(t *testing.T) {
	home := testHome(t)
	_, h, cleanup := startTestHostWithHandle(t)
	defer cleanup()
	t.Setenv("HANGAR_SUBMIT_FOCUS", "0") // isolate chunk/CR writes from focus-in writes
	w := injectWorkspace(t, h, "submit-chunk", "aider", filepath.Join(home, "wt"))
	h.mu.RLock()
	f := h.sessions[w.SessionName].(*fakeSession)
	h.mu.RUnlock()
	f.mu.Lock()
	f.bracketed = false
	f.writes = nil
	f.mu.Unlock()

	text := strings.Repeat("word ", 40) // > promptChunkRunes
	method, _ := h.workspaces.submitPrompt(w.ID, text)
	if method != submitChunk {
		t.Fatalf("method = %q, want chunk", method)
	}

	f.mu.Lock()
	writes := f.writes
	f.mu.Unlock()
	var typed strings.Builder
	chunkCount, crCount := 0, 0
	for _, wr := range writes {
		switch {
		case len(wr) == 1 && wr[0] == '\r':
			crCount++
		default:
			if strings.Contains(string(wr), bracketedPasteStart) || strings.Contains(string(wr), bracketedPasteEnd) {
				t.Fatalf("chunk fallback must not emit paste markers: %q", wr)
			}
			chunkCount++
			typed.WriteString(string(wr))
		}
	}
	if chunkCount < 2 {
		t.Fatalf("expected the prompt typed in >=2 chunks, got %d", chunkCount)
	}
	if crCount == 0 {
		t.Fatalf("expected at least one standalone CR")
	}
	if typed.String() != strings.Join(strings.Fields(text), " ") {
		t.Fatalf("typed text != normalized prompt:\n typed=%q", typed.String())
	}
}
