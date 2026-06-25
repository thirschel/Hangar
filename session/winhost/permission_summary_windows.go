//go:build windows

package winhost

import (
	"encoding/json"
	"fmt"
	"strings"

	copilot "github.com/github/copilot-sdk/go"
)

const permissionSummaryMax = 120

func permissionSummary(d *copilot.PermissionRequestedData) string {
	if d == nil {
		return ""
	}
	if summary := permissionPromptSummary(d.PromptRequest); summary != "" {
		return summary
	}
	return permissionRequestSummary(d.PermissionRequest)
}

func permissionToolName(d *copilot.PermissionRequestedData) string {
	if d == nil {
		return ""
	}
	if name := permissionPromptToolName(d.PromptRequest); name != "" {
		return name
	}
	return permissionRequestToolName(d.PermissionRequest)
}

// permissionToolCallID returns the SDK tool-call id gated by a permission request.
func permissionToolCallID(d *copilot.PermissionRequestedData) string {
	if d == nil {
		return ""
	}
	if id := permissionPromptToolCallID(d.PromptRequest); id != "" {
		return id
	}
	return permissionRequestToolCallID(d.PermissionRequest)
}

func permissionPromptSummary(req copilot.PermissionPromptRequest) string {
	switch r := req.(type) {
	case *copilot.PermissionPromptRequestCommands:
		return prefixedSummary("Run shell command", r.FullCommandText, r.Intention)
	case *copilot.PermissionPromptRequestCustomTool:
		return toolSummary("Use tool", r.ToolName, r.Args, r.ToolDescription)
	case *copilot.PermissionPromptRequestExtensionManagement:
		return prefixedSummary("Manage extension", joinNonEmpty(stringPtrValue(r.ExtensionName), r.Operation))
	case *copilot.PermissionPromptRequestExtensionPermissionAccess:
		return prefixedSummary("Grant extension access", joinNonEmpty(r.ExtensionName, strings.Join(r.Capabilities, ", ")))
	case *copilot.PermissionPromptRequestHook:
		return toolSummary("Confirm tool", r.ToolName, r.ToolArgs, stringPtrValue(r.HookMessage))
	case *copilot.PermissionPromptRequestMCP:
		args := any(nil)
		if r.Args != nil {
			args = *r.Args
		}
		name := r.ToolTitle
		if name == "" {
			name = r.ToolName
		}
		return toolSummary("Use MCP tool", joinNonEmpty(r.ServerName, name), args, "")
	case *copilot.PermissionPromptRequestMemory:
		return memorySummary(r.Action, r.Fact)
	case *copilot.PermissionPromptRequestPath:
		return prefixedSummary(fmt.Sprintf("Allow %s path access", r.AccessKind), summarizeList(r.Paths))
	case *copilot.PermissionPromptRequestRead:
		return prefixedSummary("Read path", r.Path, r.Intention)
	case *copilot.PermissionPromptRequestURL:
		return prefixedSummary("Access URL", r.URL, r.Intention)
	case *copilot.PermissionPromptRequestWrite:
		return prefixedSummary("Edit file", r.FileName, r.Intention)
	}
	return ""
}

func permissionRequestSummary(req copilot.PermissionRequest) string {
	switch r := req.(type) {
	case *copilot.PermissionRequestShell:
		return prefixedSummary("Run shell command", r.FullCommandText, r.Intention)
	case *copilot.PermissionRequestCustomTool:
		return toolSummary("Use tool", r.ToolName, r.Args, r.ToolDescription)
	case *copilot.PermissionRequestExtensionManagement:
		return prefixedSummary("Manage extension", joinNonEmpty(stringPtrValue(r.ExtensionName), r.Operation))
	case *copilot.PermissionRequestExtensionPermissionAccess:
		return prefixedSummary("Grant extension access", joinNonEmpty(r.ExtensionName, strings.Join(r.Capabilities, ", ")))
	case *copilot.PermissionRequestHook:
		return toolSummary("Confirm tool", r.ToolName, r.ToolArgs, stringPtrValue(r.HookMessage))
	case *copilot.PermissionRequestMCP:
		name := r.ToolTitle
		if name == "" {
			name = r.ToolName
		}
		return toolSummary("Use MCP tool", joinNonEmpty(r.ServerName, name), r.Args, "")
	case *copilot.PermissionRequestMemory:
		return memorySummary(r.Action, r.Fact)
	case *copilot.PermissionRequestRead:
		return prefixedSummary("Read path", r.Path, r.Intention)
	case *copilot.PermissionRequestURL:
		return prefixedSummary("Access URL", r.URL, r.Intention)
	case *copilot.PermissionRequestWrite:
		return prefixedSummary("Edit file", r.FileName, r.Intention)
	}
	return ""
}

func permissionPromptToolName(req copilot.PermissionPromptRequest) string {
	switch r := req.(type) {
	case *copilot.PermissionPromptRequestCommands:
		return "shell"
	case *copilot.PermissionPromptRequestCustomTool:
		return r.ToolName
	case *copilot.PermissionPromptRequestHook:
		return r.ToolName
	case *copilot.PermissionPromptRequestMCP:
		return r.ToolName
	case *copilot.PermissionPromptRequestRead:
		return "read"
	case *copilot.PermissionPromptRequestURL:
		return "url"
	case *copilot.PermissionPromptRequestWrite:
		return "write"
	}
	return ""
}

func permissionRequestToolName(req copilot.PermissionRequest) string {
	switch r := req.(type) {
	case *copilot.PermissionRequestShell:
		return "shell"
	case *copilot.PermissionRequestCustomTool:
		return r.ToolName
	case *copilot.PermissionRequestHook:
		return r.ToolName
	case *copilot.PermissionRequestMCP:
		return r.ToolName
	case *copilot.PermissionRequestRead:
		return "read"
	case *copilot.PermissionRequestURL:
		return "url"
	case *copilot.PermissionRequestWrite:
		return "write"
	}
	return ""
}

func permissionPromptToolCallID(req copilot.PermissionPromptRequest) string {
	switch r := req.(type) {
	case *copilot.PermissionPromptRequestCommands:
		return stringPtrValue(r.ToolCallID)
	case *copilot.PermissionPromptRequestCustomTool:
		return stringPtrValue(r.ToolCallID)
	case *copilot.PermissionPromptRequestHook:
		return stringPtrValue(r.ToolCallID)
	case *copilot.PermissionPromptRequestMCP:
		return stringPtrValue(r.ToolCallID)
	case *copilot.PermissionPromptRequestRead:
		return stringPtrValue(r.ToolCallID)
	case *copilot.PermissionPromptRequestURL:
		return stringPtrValue(r.ToolCallID)
	case *copilot.PermissionPromptRequestWrite:
		return stringPtrValue(r.ToolCallID)
	}
	return ""
}

func permissionRequestToolCallID(req copilot.PermissionRequest) string {
	switch r := req.(type) {
	case *copilot.PermissionRequestShell:
		return stringPtrValue(r.ToolCallID)
	case *copilot.PermissionRequestCustomTool:
		return stringPtrValue(r.ToolCallID)
	case *copilot.PermissionRequestHook:
		return stringPtrValue(r.ToolCallID)
	case *copilot.PermissionRequestMCP:
		return stringPtrValue(r.ToolCallID)
	case *copilot.PermissionRequestRead:
		return stringPtrValue(r.ToolCallID)
	case *copilot.PermissionRequestURL:
		return stringPtrValue(r.ToolCallID)
	case *copilot.PermissionRequestWrite:
		return stringPtrValue(r.ToolCallID)
	}
	return ""
}

func prefixedSummary(prefix string, values ...string) string {
	detail := firstNonEmpty(values...)
	if detail == "" {
		return shorten(prefix)
	}
	return shorten(prefix + ": " + detail)
}

func toolSummary(prefix, name string, args any, fallback string) string {
	label := strings.TrimSpace(name)
	if label == "" {
		label = strings.TrimSpace(fallback)
	}
	if argText := summarizeValue(args); argText != "" {
		if label == "" {
			return shorten(prefix + ": " + argText)
		}
		return shorten(prefix + " " + label + ": " + argText)
	}
	if label == "" {
		return ""
	}
	return shorten(prefix + ": " + label)
}

func memorySummary(action *copilot.PermissionRequestMemoryAction, fact string) string {
	verb := "Update memory"
	if action != nil {
		switch *action {
		case copilot.PermissionRequestMemoryActionStore:
			verb = "Store memory"
		case copilot.PermissionRequestMemoryActionVote:
			verb = "Vote on memory"
		}
	}
	return prefixedSummary(verb, fact)
}

func summarizeList(values []string) string {
	cleaned := make([]string, 0, len(values))
	for _, v := range values {
		v = strings.TrimSpace(v)
		if v != "" {
			cleaned = append(cleaned, v)
		}
	}
	if len(cleaned) == 0 {
		return ""
	}
	if len(cleaned) > 2 {
		return strings.Join(cleaned[:2], ", ") + fmt.Sprintf(" (+%d more)", len(cleaned)-2)
	}
	return strings.Join(cleaned, ", ")
}

func summarizeValue(v any) string {
	if v == nil {
		return ""
	}
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return compactLine(string(b))
}

func joinNonEmpty(parts ...string) string {
	cleaned := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			cleaned = append(cleaned, p)
		}
	}
	return strings.Join(cleaned, " ")
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		v = strings.TrimSpace(v)
		if v != "" {
			return v
		}
	}
	return ""
}

func shorten(s string) string {
	s = compactLine(s)
	if len(s) <= permissionSummaryMax {
		return s
	}
	if permissionSummaryMax <= 1 {
		return s[:permissionSummaryMax]
	}
	return strings.TrimSpace(s[:permissionSummaryMax-1]) + "…"
}

func compactLine(s string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(s)), " ")
}
