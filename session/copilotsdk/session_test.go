package copilotsdk

import (
	"os"
	"path/filepath"
	"testing"

	copilot "github.com/github/copilot-sdk/go"
)

func TestLoadMCPServers(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "mcp-config.json")
	content := `{"mcpServers":{
		"remote":{"type":"http","url":"https://example.com/mcp","headers":{"Authorization":"Bearer x","X-MCP-Toolsets":"all"}},
		"local":{"type":"local","command":"npx","args":["-y","srv"],"env":{"K":"V"}},
		"withtools":{"type":"http","url":"https://e2","tools":["only_this"]}
	}}`
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	servers, err := loadMCPServers(p)
	if err != nil {
		t.Fatalf("loadMCPServers: %v", err)
	}
	if len(servers) != 3 {
		t.Fatalf("want 3 servers, got %d", len(servers))
	}

	h, ok := servers["remote"].(copilot.MCPHTTPServerConfig)
	if !ok {
		t.Fatalf("remote: want MCPHTTPServerConfig, got %T", servers["remote"])
	}
	if h.URL != "https://example.com/mcp" {
		t.Errorf("remote URL = %q", h.URL)
	}
	if len(h.Tools) != 1 || h.Tools[0] != "*" {
		t.Errorf("remote tools should default to [*], got %v", h.Tools)
	}
	if h.Headers["Authorization"] != "Bearer x" {
		t.Errorf("remote headers not preserved: %v", h.Headers)
	}

	l, ok := servers["local"].(copilot.MCPStdioServerConfig)
	if !ok {
		t.Fatalf("local: want MCPStdioServerConfig, got %T", servers["local"])
	}
	if l.Command != "npx" || len(l.Args) != 2 || l.Env["K"] != "V" {
		t.Errorf("local command/args/env wrong: cmd=%q args=%v env=%v", l.Command, l.Args, l.Env)
	}

	wt := servers["withtools"].(copilot.MCPHTTPServerConfig)
	if len(wt.Tools) != 1 || wt.Tools[0] != "only_this" {
		t.Errorf("withtools should preserve explicit tools, got %v", wt.Tools)
	}
}

func TestLoadMCPServers_Missing(t *testing.T) {
	servers, err := loadMCPServers(filepath.Join(t.TempDir(), "nope.json"))
	if err != nil {
		t.Fatalf("missing file should be a nil error, got %v", err)
	}
	if servers != nil {
		t.Fatalf("missing file should yield a nil map, got %v", servers)
	}
}

func TestLoadMCPServers_BadJSON(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "mcp-config.json")
	if err := os.WriteFile(p, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadMCPServers(p); err == nil {
		t.Fatal("expected a parse error for malformed JSON")
	}
}

func TestStatusString(t *testing.T) {
	cases := map[Status]string{
		StatusLoading: "loading",
		StatusReady:   "ready",
		StatusRunning: "running",
		StatusWaiting: "waiting",
	}
	for st, want := range cases {
		if got := st.String(); got != want {
			t.Errorf("Status(%d).String() = %q, want %q", int(st), got, want)
		}
	}
}

func TestDecideDefaults(t *testing.T) {
	// AutoYes => approve.
	yes := New(Config{AutoYes: true})
	if approve, _ := yes.decide(nil); !approve {
		t.Error("AutoYes should approve")
	}
	// Default (AutoYes off, no Decide) => leave pending.
	no := New(Config{})
	approve, pend := no.decide(nil)
	if approve || !pend {
		t.Errorf("default should leave pending, got approve=%v pend=%v", approve, pend)
	}
	// Decide override wins.
	custom := New(Config{AutoYes: true, Decide: func(copilot.PermissionRequest) (bool, bool) { return false, false }})
	if approve, pend := custom.decide(nil); approve || pend {
		t.Errorf("Decide override should reject, got approve=%v pend=%v", approve, pend)
	}
}
