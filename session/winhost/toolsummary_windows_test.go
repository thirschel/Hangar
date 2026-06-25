//go:build windows

package winhost

import (
	"strings"
	"testing"
	"unicode/utf8"

	copilot "github.com/github/copilot-sdk/go"
	"hangar/session/winhost/proto"
)

func TestSummarizeToolArgs(t *testing.T) {
	cases := []struct {
		name string
		args any
		want string
	}{
		{"nil", nil, ""},
		{"empty map", map[string]any{}, ""},
		{"primary path", map[string]any{"path": "README.md"}, "README.md"},
		{"primary command", map[string]any{"command": "go test ./..."}, "go test ./..."},
		// path outranks query: primaryArgKeys priority order is honored.
		{"primary priority order", map[string]any{"query": "q", "path": "p"}, "p"},
		// A present-but-blank primary value is skipped, falling through to the marshal
		// (whose whitespace-only value is then collapsed by the single-line normalizer).
		{"primary blank value falls back", map[string]any{"path": "   "}, `{"path":" "}`},
		// A present-but-non-string primary value is skipped, falling through to the marshal.
		{"primary non-string falls back", map[string]any{"path": 123}, `{"path":123}`},
		// No primary key present: compact-JSON marshal with sorted keys.
		{"compact-json fallback", map[string]any{"foo": "bar", "baz": "qux"}, `{"baz":"qux","foo":"bar"}`},
		// Non-map arguments compact-JSON marshal as well.
		{"non-map slice", []any{1, 2, 3}, "[1,2,3]"},
		{"empty slice", []any{}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := summarizeToolArgs(tc.args); got != tc.want {
				t.Fatalf("summarizeToolArgs(%#v) = %q, want %q", tc.args, got, tc.want)
			}
		})
	}
}

func TestSummarizeToolArgsTruncates(t *testing.T) {
	got := summarizeToolArgs(map[string]any{"path": strings.Repeat("a", 200)})
	want := strings.Repeat("a", summaryMaxRunes) + "\u2026"
	if got != want {
		t.Fatalf("summarizeToolArgs truncation = %q, want %q", got, want)
	}
}

func TestSummarizeToolResult(t *testing.T) {
	errData := &copilot.ToolExecutionCompleteData{Error: &copilot.ToolExecutionCompleteError{Message: "boom"}}
	if got := summarizeToolResult(errData); got != "boom" {
		t.Fatalf("error path = %q, want %q", got, "boom")
	}

	// Error wins even when a Result is also present.
	errWithResult := &copilot.ToolExecutionCompleteData{
		Error:  &copilot.ToolExecutionCompleteError{Message: "boom"},
		Result: &copilot.ToolExecutionCompleteResult{Content: "ignored"},
	}
	if got := summarizeToolResult(errWithResult); got != "boom" {
		t.Fatalf("error-with-result path = %q, want %q", got, "boom")
	}

	strResult := &copilot.ToolExecutionCompleteData{
		Success: true,
		Result:  &copilot.ToolExecutionCompleteResult{Content: "150 lines read"},
	}
	if got := summarizeToolResult(strResult); got != "150 lines read" {
		t.Fatalf("string result = %q, want %q", got, "150 lines read")
	}

	// Multi-line content is collapsed to a single display line.
	multiline := &copilot.ToolExecutionCompleteData{
		Result: &copilot.ToolExecutionCompleteResult{Content: "150 lines\n  read"},
	}
	if got := summarizeToolResult(multiline); got != "150 lines read" {
		t.Fatalf("multiline result = %q, want %q", got, "150 lines read")
	}

	structured := &copilot.ToolExecutionCompleteData{
		Result: &copilot.ToolExecutionCompleteResult{StructuredContent: map[string]any{"count": 5}},
	}
	if got := summarizeToolResult(structured); got != `{"count":5}` {
		t.Fatalf("structured result = %q, want %q", got, `{"count":5}`)
	}

	if got := summarizeToolResult(nil); got != "" {
		t.Fatalf("nil data = %q, want %q", got, "")
	}

	// A successful call with no error and no usable result summarizes to "ok".
	if got := summarizeToolResult(&copilot.ToolExecutionCompleteData{Success: true}); got != "ok" {
		t.Fatalf("empty success = %q, want %q", got, "ok")
	}
	if got := summarizeToolResult(&copilot.ToolExecutionCompleteData{Result: &copilot.ToolExecutionCompleteResult{}}); got != "ok" {
		t.Fatalf("empty result = %q, want %q", got, "ok")
	}
}

func TestTruncateSummaryCountsRunes(t *testing.T) {
	// Short strings pass through untouched (after whitespace normalization).
	if got := truncateSummary("hello"); got != "hello" {
		t.Fatalf("short = %q, want %q", got, "hello")
	}
	// Truncation counts runes, not bytes: a 130-rune multi-byte string must cut at
	// 120 runes (not 120 bytes) and append exactly one ellipsis.
	multibyte := strings.Repeat("\u4e16", 130)
	got := truncateSummary(multibyte)
	want := strings.Repeat("\u4e16", summaryMaxRunes) + "\u2026"
	if got != want {
		t.Fatalf("multibyte truncation mismatch")
	}
	if n := utf8.RuneCountInString(got); n != summaryMaxRunes+1 {
		t.Fatalf("rune count = %d, want %d", n, summaryMaxRunes+1)
	}
}

func TestToolStartName(t *testing.T) {
	// Prefer ToolDescription.Name (MCP tools) over the raw ToolName.
	withDesc := &copilot.ToolExecutionStartData{
		ToolName:        "read_file",
		ToolDescription: &copilot.ToolExecutionStartToolDescription{Name: "Read"},
	}
	if got := toolStartName(withDesc); got != "Read" {
		t.Fatalf("toolStartName with description = %q, want %q", got, "Read")
	}
	// Fall back to ToolName when no description (plain tools).
	if got := toolStartName(&copilot.ToolExecutionStartData{ToolName: "read_file"}); got != "read_file" {
		t.Fatalf("toolStartName fallback = %q, want %q", got, "read_file")
	}
	if got := toolStartName(nil); got != "" {
		t.Fatalf("toolStartName(nil) = %q, want %q", got, "")
	}
}

// TestSDKEventFrameToolSummaries asserts a synthetic tool start/complete pair maps
// into frames carrying the v17 ToolArgs/ToolResult summaries and the unified tool
// name (ToolDescription.Name preferred on both start and complete).
func TestSDKEventFrameToolSummaries(t *testing.T) {
	mcp := "github"
	startFrame, ok := sdkEventFrame(copilot.SessionEvent{Data: &copilot.ToolExecutionStartData{
		ToolName:        "read_file",
		ToolCallID:      "tc1",
		Arguments:       map[string]any{"path": "README.md"},
		MCPServerName:   &mcp,
		ToolDescription: &copilot.ToolExecutionStartToolDescription{Name: "Read"},
	}})
	if !ok {
		t.Fatal("sdkEventFrame did not map tool start")
	}
	if startFrame.Kind != proto.EventKindToolStart || startFrame.ToolName != "Read" ||
		startFrame.ToolCallID != "tc1" || startFrame.ToolArgs != "README.md" || startFrame.MCPServer != "github" {
		t.Fatalf("tool start frame = %+v", startFrame)
	}

	completeFrame, ok := sdkEventFrame(copilot.SessionEvent{Data: &copilot.ToolExecutionCompleteData{
		ToolCallID:      "tc1",
		Success:         true,
		Result:          &copilot.ToolExecutionCompleteResult{Content: "150 lines read"},
		ToolDescription: &copilot.ToolExecutionCompleteToolDescription{Name: "Read"},
	}})
	if !ok {
		t.Fatal("sdkEventFrame did not map tool complete")
	}
	if completeFrame.Kind != proto.EventKindToolComplete || completeFrame.ToolName != "Read" ||
		completeFrame.ToolCallID != "tc1" || completeFrame.ToolResult != "150 lines read" {
		t.Fatalf("tool complete frame = %+v", completeFrame)
	}
}
