package copilotsdk

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

func TestMCPServerNamesAccessor(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "mcp-config.json")
	content := `{"mcpServers":{
		"zeta":{"type":"http","url":"https://example.com/z","headers":{"Authorization":"Bearer secret"}},
		"alpha":{"type":"http","url":"https://example.com/a"}
	}}`
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	s := New(Config{MCPConfigPath: p})
	cfg := s.sessionConfig()
	if len(cfg.MCPServers) != 2 {
		t.Fatalf("sessionConfig MCPServers len = %d, want 2", len(cfg.MCPServers))
	}
	names := s.MCPServerNames()
	if len(names) != 2 || names[0] != "alpha" || names[1] != "zeta" {
		t.Fatalf("MCPServerNames = %v, want [alpha zeta]", names)
	}
	names[0] = "mutated"
	if got := s.MCPServerNames(); got[0] != "alpha" {
		t.Fatalf("MCPServerNames returned an alias: %v", got)
	}

	disabled := New(Config{MCPConfigPath: p, DisableMCP: true})
	disabled.sessionConfig()
	if got := disabled.MCPServerNames(); got != nil {
		t.Fatalf("disabled MCPServerNames = %v, want nil", got)
	}
}

// TestExtraMCPServersOverlay proves the Hangar catalog (Config.ExtraMCPServers) is
// unioned on top of the CLI's mcp-config.json base set, and that on a name
// collision the CATALOG WINS (replaces the base entry). Both contribute to
// MCPServerNames().
func TestExtraMCPServersOverlay(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "mcp-config.json")
	// Base CLI config: "base" (http) and "shared" (http) — "shared" collides with
	// a catalog entry below.
	content := `{"mcpServers":{
		"base":{"type":"http","url":"https://example.com/base"},
		"shared":{"type":"http","url":"https://example.com/cli-shared"}
	}}`
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	extra := map[string]copilot.MCPServerConfig{
		// Collides with the base "shared": the catalog (stdio) entry must win.
		"shared":      copilot.MCPStdioServerConfig{Command: "catalog-cmd", Tools: []string{"*"}},
		"catalogonly": copilot.MCPStdioServerConfig{Command: "only", Tools: []string{"*"}},
	}
	s := New(Config{MCPConfigPath: p, ExtraMCPServers: extra})
	cfg := s.sessionConfig()

	// Union: base + shared + catalogonly = 3 (shared de-duplicated).
	if len(cfg.MCPServers) != 3 {
		t.Fatalf("sessionConfig MCPServers len = %d, want 3 (union)", len(cfg.MCPServers))
	}
	// Collision: "shared" is the catalog stdio entry, NOT the base http one.
	shared, ok := cfg.MCPServers["shared"].(copilot.MCPStdioServerConfig)
	if !ok {
		t.Fatalf("shared: catalog should win, want MCPStdioServerConfig, got %T", cfg.MCPServers["shared"])
	}
	if shared.Command != "catalog-cmd" {
		t.Errorf("shared command = %q, want catalog-cmd (catalog must override base)", shared.Command)
	}
	// The non-colliding base server is preserved.
	if _, ok := cfg.MCPServers["base"].(copilot.MCPHTTPServerConfig); !ok {
		t.Errorf("base server should be preserved as MCPHTTPServerConfig, got %T", cfg.MCPServers["base"])
	}

	// All three appear (sorted) in MCPServerNames().
	names := s.MCPServerNames()
	if len(names) != 3 || names[0] != "base" || names[1] != "catalogonly" || names[2] != "shared" {
		t.Fatalf("MCPServerNames = %v, want [base catalogonly shared]", names)
	}
}

// TestExtraMCPServersNoBaseConfig proves the catalog still forwards when there is
// no CLI mcp-config.json at all (the base load yields a nil map).
func TestExtraMCPServersNoBaseConfig(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "nope.json")
	extra := map[string]copilot.MCPServerConfig{
		"catalogonly": copilot.MCPStdioServerConfig{Command: "only", Tools: []string{"*"}},
	}
	s := New(Config{MCPConfigPath: missing, ExtraMCPServers: extra})
	cfg := s.sessionConfig()
	if len(cfg.MCPServers) != 1 {
		t.Fatalf("sessionConfig MCPServers len = %d, want 1 (catalog only)", len(cfg.MCPServers))
	}
	if got := s.MCPServerNames(); len(got) != 1 || got[0] != "catalogonly" {
		t.Fatalf("MCPServerNames = %v, want [catalogonly]", got)
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

func TestRespondPermissionNotStarted(t *testing.T) {
	s := New(Config{})
	err := s.RespondPermission(context.Background(), "perm-1", true)
	if err == nil || !strings.Contains(err.Error(), "session not started") {
		t.Fatalf("RespondPermission error = %v, want not-started error", err)
	}
}

func TestUserInputPromptFlow(t *testing.T) {
	prompts := make(chan Prompt, 1)
	s := New(Config{OnPrompt: func(p Prompt) { prompts <- p }})
	allowFreeform := true
	done := make(chan struct {
		resp copilot.UserInputResponse
		err  error
	}, 1)

	go func() {
		resp, err := s.onUserInput(copilot.UserInputRequest{
			Question:      "Pick one",
			Choices:       []string{"A", "B"},
			AllowFreeform: &allowFreeform,
		}, copilot.UserInputInvocation{})
		done <- struct {
			resp copilot.UserInputResponse
			err  error
		}{resp: resp, err: err}
	}()

	var prompt Prompt
	select {
	case prompt = <-prompts:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for prompt")
	}
	if prompt.Kind != "user_input" || prompt.RequestID == "" || prompt.Question != "Pick one" || !prompt.AllowFreeform {
		t.Fatalf("prompt = %+v", prompt)
	}
	if len(prompt.Choices) != 2 || prompt.Choices[0] != "A" || prompt.Choices[1] != "B" {
		t.Fatalf("prompt choices = %v", prompt.Choices)
	}
	if err := s.RespondUserInput(prompt.RequestID, "B", false); err != nil {
		t.Fatalf("RespondUserInput: %v", err)
	}

	select {
	case got := <-done:
		if got.err != nil {
			t.Fatalf("onUserInput returned error: %v", got.err)
		}
		if got.resp.Answer != "B" || got.resp.WasFreeform {
			t.Fatalf("response = %+v", got.resp)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for onUserInput response")
	}
}

func TestCloseUnblocksPendingUserInput(t *testing.T) {
	prompts := make(chan Prompt, 1)
	s := New(Config{OnPrompt: func(p Prompt) { prompts <- p }})
	done := make(chan error, 1)

	go func() {
		_, err := s.onUserInput(copilot.UserInputRequest{Question: "Continue?"}, copilot.UserInputInvocation{})
		done <- err
	}()

	select {
	case <-prompts:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for prompt")
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "session closed") {
			t.Fatalf("onUserInput error = %v, want session closed error", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Close did not unblock onUserInput")
	}
}

func TestAbortUnblocksPendingUserInput(t *testing.T) {
	prompts := make(chan Prompt, 1)
	s := New(Config{OnPrompt: func(p Prompt) { prompts <- p }})
	done := make(chan error, 1)

	go func() {
		_, err := s.onUserInput(copilot.UserInputRequest{Question: "Continue?"}, copilot.UserInputInvocation{})
		done <- err
	}()

	select {
	case <-prompts:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for prompt")
	}
	// A mid-turn Abort must unblock the parked ask_user handler WITHOUT closing the
	// session (the session stays reusable for the next turn).
	s.abortPrompts()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("onUserInput should return an error when its turn is aborted")
		}
	case <-time.After(time.Second):
		t.Fatal("abortPrompts did not unblock onUserInput")
	}
	if s.closing {
		t.Fatal("abortPrompts must not mark the session closing")
	}
}

func TestIsProcessExited(t *testing.T) {
	cases := map[string]bool{
		"failed to send message: CLI process exited: exit status 1": true,
		"CLI process exited unexpectedly":                           true,
		"process exited unexpectedly":                               true,
		"client stopped":                                            false,
		"context deadline exceeded":                                 false,
	}
	for msg, want := range cases {
		if got := isProcessExited(errors.New(msg)); got != want {
			t.Fatalf("isProcessExited(%q) = %v, want %v", msg, got, want)
		}
	}
	if isProcessExited(nil) {
		t.Fatal("isProcessExited(nil) must be false")
	}
}

func TestNoteErrMarksExitedSticky(t *testing.T) {
	s := New(Config{})
	if s.noteErr(nil); s.Exited() {
		t.Fatal("nil error must not mark the session exited")
	}
	if s.noteErr(errors.New("transient blip")); s.Exited() {
		t.Fatal("non-process error must not mark exited")
	}
	s.noteErr(errors.New("failed to send message: CLI process exited: exit status 1"))
	if !s.Exited() {
		t.Fatal("a process-exited error must mark the session exited")
	}
	// StatusExited is terminal/sticky: a later status update must not clear it.
	s.setStatus(StatusRunning)
	if !s.Exited() || s.Status() != StatusExited {
		t.Fatalf("StatusExited must be sticky, got %v", s.Status())
	}
}
