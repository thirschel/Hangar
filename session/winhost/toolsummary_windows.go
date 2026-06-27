//go:build windows

package winhost

import (
	"encoding/json"
	"strings"

	copilot "github.com/github/copilot-sdk/go"
)

// summaryMaxRunes caps the length of a forwarded tool args/result summary. The
// rich event stream is display data, and a tool's full arguments or result can be
// an entire file's contents, so every summary is truncated to this many runes
// (with a trailing ellipsis) rather than forwarded verbatim.
const summaryMaxRunes = 120

// primaryArgKeys are the well-known argument names, in priority order, whose value
// (when a non-empty string) best summarizes a tool call - mirroring how the CLI
// renders a tool line: a read shows its path, a shell its command, a search its
// query/pattern, and so on. The first present, non-empty string value wins; with
// no match the whole arguments object is compact-JSON-marshaled instead.
var primaryArgKeys = []string{"path", "file", "filePath", "query", "pattern", "command", "cmd", "url", "name"}

// summarizeToolArgs renders a concise, single-line summary of a tool call's
// arguments for the rich event stream (EventKindToolStart). When the arguments are
// a map[string]any it prefers the value of the first matching primaryArgKeys entry;
// otherwise it compact-JSON-marshals the arguments. The result is whitespace
// collapsed and truncated to summaryMaxRunes. Returns "" for nil or empty arguments.
func summarizeToolArgs(args any) string {
	if args == nil {
		return ""
	}
	if m, ok := args.(map[string]any); ok {
		if len(m) == 0 {
			return ""
		}
		for _, key := range primaryArgKeys {
			if v, ok := m[key]; ok {
				if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
					return truncateSummary(s)
				}
			}
		}
	}
	b, err := json.Marshal(args)
	if err != nil {
		return ""
	}
	return truncateSummary(blankIfEmptyJSON(string(b)))
}

// summarizeToolResult renders a concise, single-line summary of a completed tool
// call for the rich event stream (EventKindToolComplete). A failed call summarizes
// to its error message; a successful call to the SDK's own concise result text
// (ToolExecutionCompleteResult.Content), falling back to a compact-JSON marshal of
// any structured content, and finally to "ok" when the SDK reported no usable
// result. Returns "" only when data itself is nil. Everything is truncated to
// summaryMaxRunes. It deliberately never marshals the whole result wrapper, which
// can embed full diffs or base64 binary results.
func summarizeToolResult(data *copilot.ToolExecutionCompleteData) string {
	if data == nil {
		return ""
	}
	if data.Error != nil {
		return truncateSummary(data.Error.Message)
	}
	if r := data.Result; r != nil {
		if strings.TrimSpace(r.Content) != "" {
			return truncateSummary(r.Content)
		}
		if r.StructuredContent != nil {
			if b, err := json.Marshal(r.StructuredContent); err == nil {
				if s := blankIfEmptyJSON(string(b)); s != "" {
					return truncateSummary(s)
				}
			}
		}
	}
	return "ok"
}

// truncateSummary normalizes s for single-line display - collapsing every run of
// whitespace (including newlines) to a single space and trimming the ends - then
// caps it at summaryMaxRunes runes, appending a horizontal ellipsis when it had to
// cut. Counting runes (not bytes) keeps multi-byte text from being split mid-rune.
func truncateSummary(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	r := []rune(s)
	if len(r) <= summaryMaxRunes {
		return s
	}
	return string(r[:summaryMaxRunes]) + "\u2026"
}

// blankIfEmptyJSON maps the compact-JSON encodings of an empty value to "" so an
// empty arguments/result object never forwards a meaningless "{}"/"null" summary.
func blankIfEmptyJSON(s string) string {
	switch s {
	case "null", "{}", "[]", `""`:
		return ""
	}
	return s
}
