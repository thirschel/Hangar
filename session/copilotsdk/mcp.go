package copilotsdk

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"

	copilot "github.com/github/copilot-sdk/go"
)

// mcpConfigPath resolves the copilot CLI's mcp-config.json path for this session.
func (s *Session) mcpConfigPath() string {
	if s.cfg.MCPConfigPath != "" {
		return s.cfg.MCPConfigPath
	}
	return defaultMCPConfigPath()
}

func defaultMCPConfigPath() string {
	home := ""
	if runtime.GOOS == "windows" {
		home = os.Getenv("USERPROFILE")
	}
	if home == "" {
		if d, err := os.UserHomeDir(); err == nil {
			home = d
		}
	}
	return filepath.Join(home, ".copilot", "mcp-config.json")
}

// mcpConfigFile mirrors the subset of the copilot mcp-config.json schema we forward.
type mcpConfigFile struct {
	MCPServers map[string]mcpServerEntry `json:"mcpServers"`
}

type mcpServerEntry struct {
	Type    string            `json:"type"`
	URL     string            `json:"url"`
	Command string            `json:"command"`
	Args    []string          `json:"args"`
	Env     map[string]string `json:"env"`
	Headers map[string]string `json:"headers"`
	Tools   []string          `json:"tools"`
}

// loadMCPServers parses mcp-config.json and converts each entry into an SDK
// MCPServerConfig. A missing file is not an error (returns nil). The copilot CLI
// rejects a server with no tools list, so an empty/absent tools field defaults to
// ["*"] (verified in the Phase 0 spikes).
func loadMCPServers(path string) (map[string]copilot.MCPServerConfig, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var cfg mcpConfigFile
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	out := make(map[string]copilot.MCPServerConfig, len(cfg.MCPServers))
	for name, e := range cfg.MCPServers {
		tools := e.Tools
		if len(tools) == 0 {
			tools = []string{"*"}
		}
		switch {
		case e.URL != "" || e.Type == "http" || e.Type == "sse":
			out[name] = copilot.MCPHTTPServerConfig{URL: e.URL, Headers: e.Headers, Tools: tools}
		case e.Command != "":
			out[name] = copilot.MCPStdioServerConfig{Command: e.Command, Args: e.Args, Env: e.Env, Tools: tools}
		}
	}
	return out, nil
}

func sortedMCPServerNames(servers map[string]copilot.MCPServerConfig) []string {
	if len(servers) == 0 {
		return nil
	}
	names := make([]string, 0, len(servers))
	for name := range servers {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// mcpConfiguredTools parses mcp-config.json and returns the explicitly configured
// tool allowlist per server name, for the best-effort Tools field on the rich MCP
// page. Servers with no tools list (the CLI's implicit "all tools" default) are
// omitted, and a lone "*" wildcard is treated as "unknown" since it is not a real
// tool name. It is purely advisory: a missing/malformed file yields a nil map.
func mcpConfiguredTools(path string) map[string][]string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var cfg mcpConfigFile
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return nil
	}
	out := make(map[string][]string, len(cfg.MCPServers))
	for name, e := range cfg.MCPServers {
		if tools := explicitTools(e.Tools); len(tools) > 0 {
			out[name] = tools
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// explicitTools filters a configured tools list down to real tool names, dropping
// the empty and "*" (all-tools wildcard) entries that are not displayable names.
func explicitTools(tools []string) []string {
	out := make([]string, 0, len(tools))
	for _, t := range tools {
		if t == "" || t == "*" {
			continue
		}
		out = append(out, t)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
