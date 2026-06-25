package copilotsdk

import (
	"os"
	"path/filepath"
	"testing"

	copilot "github.com/github/copilot-sdk/go"
)

// TestCaptureMCPServersLoadedAccessor drives a synthetic servers-loaded event
// through handleEvent and asserts the captured detail (status/transport/source/
// error) plus the best-effort tool allowlist merged from config.
func TestCaptureMCPServersLoadedAccessor(t *testing.T) {
	s := New(Config{})
	s.setMCPTools(map[string][]string{"github": {"read_issue", "create_pr"}})

	transport := copilot.MCPServerTransportStdio
	source := copilot.MCPServerSourceUser
	failedMsg := "handshake failed"
	s.handleEvent(copilot.SessionEvent{Data: &copilot.SessionMCPServersLoadedData{
		Servers: []copilot.MCPServersLoadedServer{
			{Name: "github", Status: copilot.MCPServerStatusConnected, Transport: &transport, Source: &source},
			{Name: "broken", Status: copilot.MCPServerStatusFailed, Error: &failedMsg},
		},
	}})

	got := s.MCPServers()
	if len(got) != 2 {
		t.Fatalf("MCPServers len = %d, want 2", len(got))
	}
	gh := got[0]
	if gh.Name != "github" || gh.Status != "connected" || gh.Transport != "stdio" || gh.Source != "user" || gh.Error != "" {
		t.Fatalf("github detail = %+v", gh)
	}
	if len(gh.Tools) != 2 || gh.Tools[0] != "read_issue" || gh.Tools[1] != "create_pr" {
		t.Fatalf("github tools = %v", gh.Tools)
	}
	br := got[1]
	if br.Name != "broken" || br.Status != "failed" || br.Error != "handshake failed" || br.Transport != "" || br.Source != "" {
		t.Fatalf("broken detail = %+v", br)
	}
	if br.Tools != nil {
		t.Fatalf("broken tools should be nil, got %v", br.Tools)
	}
}

// TestCaptureMCPServerStatusChanged asserts a status change updates the matching
// server in place (preserving order) and that a status for an unknown server is
// appended best-effort, so the whole list is always available.
func TestCaptureMCPServerStatusChanged(t *testing.T) {
	s := New(Config{})
	s.handleEvent(copilot.SessionEvent{Data: &copilot.SessionMCPServersLoadedData{
		Servers: []copilot.MCPServersLoadedServer{
			{Name: "github", Status: copilot.MCPServerStatusPending},
			{Name: "docs", Status: copilot.MCPServerStatusPending},
		},
	}})

	errMsg := "auth required"
	s.handleEvent(copilot.SessionEvent{Data: &copilot.SessionMCPServerStatusChangedData{
		ServerName: "github", Status: copilot.MCPServerStatusNeedsAuth, Error: &errMsg,
	}})
	got := s.MCPServers()
	if len(got) != 2 || got[0].Name != "github" || got[0].Status != "needs-auth" || got[0].Error != "auth required" {
		t.Fatalf("after status change = %+v", got)
	}
	if got[1].Name != "docs" || got[1].Status != "pending" {
		t.Fatalf("docs should be unchanged = %+v", got[1])
	}

	s.handleEvent(copilot.SessionEvent{Data: &copilot.SessionMCPServerStatusChangedData{
		ServerName: "extra", Status: copilot.MCPServerStatusConnected,
	}})
	got = s.MCPServers()
	if len(got) != 3 || got[2].Name != "extra" || got[2].Status != "connected" {
		t.Fatalf("unknown server should be appended = %+v", got)
	}
}

// TestCaptureSkillsLoadedAccessor drives a synthetic skills-loaded event and
// asserts the captured skill detail (UserInvocable is intentionally dropped).
func TestCaptureSkillsLoadedAccessor(t *testing.T) {
	s := New(Config{})
	path := filepath.FromSlash("/home/u/.copilot/skills/foo.md")
	s.handleEvent(copilot.SessionEvent{Data: &copilot.SessionSkillsLoadedData{
		Skills: []copilot.SkillsLoadedSkill{
			{Name: "foo", Description: "does foo", Enabled: true, Source: copilot.SkillSourceProject, Path: &path, UserInvocable: true},
			{Name: "bar", Enabled: false, Source: copilot.SkillSourcePersonalCopilot},
		},
	}})

	got := s.Skills()
	if len(got) != 2 {
		t.Fatalf("Skills len = %d, want 2", len(got))
	}
	if got[0].Name != "foo" || got[0].Description != "does foo" || !got[0].Enabled || got[0].Source != "project" || got[0].Path != path {
		t.Fatalf("foo skill = %+v", got[0])
	}
	if got[1].Name != "bar" || got[1].Enabled || got[1].Source != "personal-copilot" || got[1].Path != "" {
		t.Fatalf("bar skill = %+v", got[1])
	}
}

// TestDetailAccessorsCopySafe ensures the accessors return defensive copies so a
// caller cannot mutate the session's captured state through the returned slices.
func TestDetailAccessorsCopySafe(t *testing.T) {
	s := New(Config{})
	s.setMCPTools(map[string][]string{"github": {"read"}})
	s.handleEvent(copilot.SessionEvent{Data: &copilot.SessionMCPServersLoadedData{
		Servers: []copilot.MCPServersLoadedServer{{Name: "github", Status: copilot.MCPServerStatusConnected}},
	}})
	s.handleEvent(copilot.SessionEvent{Data: &copilot.SessionSkillsLoadedData{
		Skills: []copilot.SkillsLoadedSkill{{Name: "foo", Source: copilot.SkillSourceProject}},
	}})

	mcp := s.MCPServers()
	mcp[0].Name = "mutated"
	mcp[0].Tools[0] = "mutated"
	if again := s.MCPServers(); again[0].Name != "github" || again[0].Tools[0] != "read" {
		t.Fatalf("MCPServers accessor returned an alias: %+v", again[0])
	}

	sk := s.Skills()
	sk[0].Name = "mutated"
	if again := s.Skills(); again[0].Name != "foo" {
		t.Fatalf("Skills accessor returned an alias: %+v", again[0])
	}
}

func TestEmptyDetailAccessorsReturnNil(t *testing.T) {
	s := New(Config{})
	if s.MCPServers() != nil {
		t.Fatal("MCPServers on a fresh session should be nil")
	}
	if s.Skills() != nil {
		t.Fatal("Skills on a fresh session should be nil")
	}
}

// TestMCPConfiguredTools covers the best-effort tool allowlist parse: explicit
// tool names are kept, the "*" wildcard and empty lists are dropped, and a
// missing/malformed file yields nil.
func TestMCPConfiguredTools(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "mcp-config.json")
	content := `{"mcpServers":{
		"explicit":{"type":"http","url":"https://x","tools":["read_issue","create_pr"]},
		"wildcard":{"type":"http","url":"https://y","tools":["*"]},
		"none":{"type":"http","url":"https://z"}
	}}`
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	tools := mcpConfiguredTools(p)
	if len(tools) != 1 {
		t.Fatalf("mcpConfiguredTools = %v, want only the explicit server", tools)
	}
	if got := tools["explicit"]; len(got) != 2 || got[0] != "read_issue" || got[1] != "create_pr" {
		t.Fatalf("explicit tools = %v", got)
	}
	if _, ok := tools["wildcard"]; ok {
		t.Fatalf("wildcard-only tools should be dropped: %v", tools)
	}
	if _, ok := tools["none"]; ok {
		t.Fatalf("absent tools should be dropped: %v", tools)
	}
	if mcpConfiguredTools(filepath.Join(dir, "missing.json")) != nil {
		t.Fatal("missing file should yield nil tools")
	}
}

// TestForwardedMCPServersMergesToolsIntoDetail wires the real config parse into a
// captured servers-loaded snapshot end-to-end within copilotsdk.
func TestForwardedMCPServersMergesToolsIntoDetail(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "mcp-config.json")
	content := `{"mcpServers":{"github":{"type":"http","url":"https://x","tools":["read_issue"]}}}`
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	s := New(Config{MCPConfigPath: p})
	s.forwardedMCPServers() // parse config: sets mcpServers + mcpTools

	s.handleEvent(copilot.SessionEvent{Data: &copilot.SessionMCPServersLoadedData{
		Servers: []copilot.MCPServersLoadedServer{{Name: "github", Status: copilot.MCPServerStatusConnected}},
	}})
	got := s.MCPServers()
	if len(got) != 1 || got[0].Name != "github" || len(got[0].Tools) != 1 || got[0].Tools[0] != "read_issue" {
		t.Fatalf("detail with merged tools = %+v", got)
	}
}
