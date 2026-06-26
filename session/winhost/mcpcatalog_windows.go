//go:build windows

package winhost

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	copilot "github.com/github/copilot-sdk/go"
	"hangar/config"
)

// mcpCatalogTimeoutMax clamps a per-server MCP timeout (in seconds) to a sane
// ceiling so a tampered/typo'd catalog can't request an effectively-infinite
// startup timeout. 0 means "unset" (the SDK/CLI default).
const mcpCatalogTimeoutMax = 600

// mcpCatalog is the parsed ~/.hangar/mcp.json: a global server catalog plus the
// per-repo enablement list. It is the Hangar-owned MCP configuration for the rich
// (Copilot SDK) agent view, distinct from the copilot CLI's own
// ~/.copilot/mcp-config.json (which copilotsdk.loadMCPServers forwards). The
// schema is shared with the desktop client, which writes this file.
type mcpCatalog struct {
	Servers     map[string]mcpCatalogServer `json:"servers"`
	RepoEnabled map[string][]string         `json:"repoEnabled"`
}

// mcpCatalogServer mirrors one entry of the shared mcp.json schema. Type is
// "local" | "http" ("sse" is accepted on read and treated as http). Timeout is in
// seconds (0 = unset). Cwd maps to the SDK's WorkingDirectory (json "cwd"). Both
// the stdio (Command/Args/Env/Cwd) and http (URL/Headers) field sets share one
// struct; toSDKConfig picks the transport.
type mcpCatalogServer struct {
	Type    string            `json:"type"`
	Command string            `json:"command"`
	Args    []string          `json:"args"`
	Env     map[string]string `json:"env"`
	Cwd     string            `json:"cwd"`
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers"`
	Tools   []string          `json:"tools"`
	Timeout int               `json:"timeout"`
}

// canonRepoKey canonicalizes a repo path into the stable key used to look up
// per-repo MCP enablement (mcpCatalog.RepoEnabled) and reported on the wire as
// WorkspaceInfo.RepoKey. It is daemon-owned and reconciles the inconsistent
// stored repoPath forms — `git rev-parse --show-toplevel` yields forward slashes
// while an in-place filepath.Clean yields backslashes — by cleaning, normalizing
// separators to forward slashes, and lowercasing (Windows paths are
// case-insensitive). Empty in -> empty out.
func canonRepoKey(p string) string {
	if p == "" {
		return ""
	}
	return strings.ToLower(filepath.ToSlash(filepath.Clean(p)))
}

// mcpCatalogPath resolves ~/.hangar/mcp.json (config.GetConfigDir() = ~/.hangar).
func mcpCatalogPath() (string, error) {
	dir, err := config.GetConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "mcp.json"), nil
}

// loadMCPCatalog reads ~/.hangar/mcp.json fresh on every call (so catalog edits
// take effect on the next session start, matching the copilot CLI forwarding
// semantics). A missing file is not an error (returns the zero catalog); a
// malformed file is.
func loadMCPCatalog() (mcpCatalog, error) {
	var cat mcpCatalog
	path, err := mcpCatalogPath()
	if err != nil {
		return cat, err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cat, nil
		}
		return cat, fmt.Errorf("read %s: %w", path, err)
	}
	if err := json.Unmarshal(raw, &cat); err != nil {
		return cat, fmt.Errorf("parse %s: %w", path, err)
	}
	return cat, nil
}

// enabledMCPFor returns the catalog MCP servers enabled for repoPath, converted to
// SDK MCPServerConfig values keyed by server name. The lookup key is
// canonRepoKey(repoPath). It is a method on the workspace manager so a malformed
// catalog is logged through the host logger (best effort) rather than silently
// dropped; the result is otherwise independent of manager state. An empty
// repoPath, a missing catalog file, or no enabled-and-defined servers yields nil.
// Defensive against nil maps and a nil receiver/host (unit tests).
func (m *workspaceManager) enabledMCPFor(repoPath string) map[string]copilot.MCPServerConfig {
	if repoPath == "" {
		return nil
	}
	cat, err := loadMCPCatalog()
	if err != nil {
		if m != nil && m.host != nil && m.host.logger != nil {
			m.host.logger.Printf("mcp catalog: %v (continuing without catalog servers)", err)
		}
		return nil
	}
	if len(cat.Servers) == 0 || len(cat.RepoEnabled) == 0 {
		return nil
	}
	names := cat.RepoEnabled[canonRepoKey(repoPath)]
	if len(names) == 0 {
		return nil
	}
	out := make(map[string]copilot.MCPServerConfig, len(names))
	for _, name := range names {
		srv, ok := cat.Servers[name]
		if !ok {
			continue
		}
		if cfg, ok := srv.toSDKConfig(); ok {
			out[name] = cfg
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// toSDKConfig converts a catalog server entry into an SDK MCPServerConfig. The
// explicit "type" wins; when it is empty/unknown the transport is inferred from
// the presence of url (http) or command (stdio). A stdio server maps cwd ->
// WorkingDirectory and clamps timeout to [0,600] seconds; a remote server carries
// headers and the same clamped timeout. Tools default to ["*"] (the CLI rejects an
// empty tools list). An entry that is neither (no command and no url) yields
// ok=false and is skipped.
func (e mcpCatalogServer) toSDKConfig() (copilot.MCPServerConfig, bool) {
	tools := e.Tools
	if len(tools) == 0 {
		tools = []string{"*"}
	}
	timeout := e.Timeout
	if timeout < 0 {
		timeout = 0
	}
	if timeout > mcpCatalogTimeoutMax {
		timeout = mcpCatalogTimeoutMax
	}
	switch {
	case e.Type == "http" || e.Type == "sse":
		return copilot.MCPHTTPServerConfig{URL: e.URL, Headers: e.Headers, Tools: tools, Timeout: timeout}, true
	case e.Type == "local":
		return copilot.MCPStdioServerConfig{Command: e.Command, Args: e.Args, Env: e.Env, Tools: tools, WorkingDirectory: e.Cwd, Timeout: timeout}, true
	case e.URL != "":
		return copilot.MCPHTTPServerConfig{URL: e.URL, Headers: e.Headers, Tools: tools, Timeout: timeout}, true
	case e.Command != "":
		return copilot.MCPStdioServerConfig{Command: e.Command, Args: e.Args, Env: e.Env, Tools: tools, WorkingDirectory: e.Cwd, Timeout: timeout}, true
	default:
		return nil, false
	}
}
