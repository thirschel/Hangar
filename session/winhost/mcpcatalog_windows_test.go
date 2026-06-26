//go:build windows

package winhost

import (
	"os"
	"path/filepath"
	"testing"

	copilot "github.com/github/copilot-sdk/go"
)

func TestCanonRepoKey(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"", ""},
		{"C:/Work/Repo", "c:/work/repo"},
		{`C:\Work\repo`, "c:/work/repo"},
		{`C:\Work\Repo\.`, "c:/work/repo"},
		{"C:/Work/Repo/sub/..", "c:/work/repo"},
	}
	for _, tc := range cases {
		if got := canonRepoKey(tc.in); got != tc.want {
			t.Errorf("canonRepoKey(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
	// The two stored repoPath forms (forward slashes from `git rev-parse
	// --show-toplevel` vs backslashes from in-place filepath.Clean) must reconcile
	// to the same key, otherwise per-repo enablement silently misses.
	if a, b := canonRepoKey("C:/x/Repo"), canonRepoKey(`C:\x\repo`); a != b {
		t.Fatalf("canonRepoKey did not reconcile slash/case forms: %q vs %q", a, b)
	}
}

func writeMCPCatalog(t *testing.T, home, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(home, ".hangar", "mcp.json"), []byte(body), 0o600); err != nil {
		t.Fatalf("write mcp.json: %v", err)
	}
}

func mcpKeysOf(m map[string]copilot.MCPServerConfig) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func TestEnabledMCPFor(t *testing.T) {
	home := testHome(t) // sets USERPROFILE to a temp home and creates ~/.hangar
	repo := `C:\Work\MyRepo`

	// stdio1 + remote1 are enabled for this repo; "ghost" is enabled but undefined
	// (must be skipped); "other" is defined but enabled for a different repo.
	writeMCPCatalog(t, home, `{
	  "servers": {
	    "stdio1":  {"type":"local","command":"srv","args":["--x"],"env":{"K":"V"},"cwd":"C:/tools","timeout":5},
	    "remote1": {"type":"sse","url":"https://example.com/mcp","headers":{"Authorization":"Bearer t"},"timeout":1000},
	    "other":   {"type":"local","command":"nope"}
	  },
	  "repoEnabled": {
	    "c:/work/myrepo": ["stdio1","remote1","ghost"],
	    "c:/other":       ["other"]
	  }
	}`)

	m := &workspaceManager{}
	got := m.enabledMCPFor(repo)
	if len(got) != 2 {
		t.Fatalf("enabledMCPFor returned %d servers, want 2 (%v)", len(got), mcpKeysOf(got))
	}

	// stdio1: cwd -> WorkingDirectory; timeout (<=600) passes through; tools default to [*].
	stdio, ok := got["stdio1"].(copilot.MCPStdioServerConfig)
	if !ok {
		t.Fatalf("stdio1: want MCPStdioServerConfig, got %T", got["stdio1"])
	}
	if stdio.Command != "srv" || stdio.WorkingDirectory != "C:/tools" || stdio.Timeout != 5 {
		t.Errorf("stdio1 mapped wrong: cmd=%q cwd=%q timeout=%d", stdio.Command, stdio.WorkingDirectory, stdio.Timeout)
	}
	if len(stdio.Tools) != 1 || stdio.Tools[0] != "*" {
		t.Errorf("stdio1 tools should default to [*], got %v", stdio.Tools)
	}

	// remote1: sse -> http; timeout clamped to 600.
	remote, ok := got["remote1"].(copilot.MCPHTTPServerConfig)
	if !ok {
		t.Fatalf("remote1: want MCPHTTPServerConfig (sse->http), got %T", got["remote1"])
	}
	if remote.URL != "https://example.com/mcp" || remote.Timeout != 600 {
		t.Errorf("remote1 mapped wrong: url=%q timeout=%d (want timeout clamped to 600)", remote.URL, remote.Timeout)
	}
	if remote.Headers["Authorization"] != "Bearer t" {
		t.Errorf("remote1 headers not preserved: %v", remote.Headers)
	}

	// A repo with no enablement -> nil; empty repoPath -> nil.
	if got := m.enabledMCPFor(`C:\Work\Unrelated`); got != nil {
		t.Errorf("unrelated repo should yield nil, got %v", mcpKeysOf(got))
	}
	if got := m.enabledMCPFor(""); got != nil {
		t.Errorf("empty repoPath should yield nil, got %v", mcpKeysOf(got))
	}
}

func TestEnabledMCPForMissingFile(t *testing.T) {
	testHome(t) // temp home with ~/.hangar but no mcp.json
	m := &workspaceManager{}
	if got := m.enabledMCPFor(`C:\Work\Repo`); got != nil {
		t.Fatalf("missing catalog should yield nil, got %v", mcpKeysOf(got))
	}
}
