//go:build windows

package winhost

import (
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"claude-squad/session/promptpolicy"
	"claude-squad/session/winhost/proto"
)

// Shared control client. The session host stays alive while the TUI holds this
// connection (and while sessions exist), so the connection is long-lived.
var (
	clientMu     sync.Mutex
	sharedClient *Client
)

func getClient() (*Client, error) {
	clientMu.Lock()
	defer clientMu.Unlock()
	if sharedClient != nil {
		return sharedClient, nil
	}
	c, err := EnsureHost()
	if err != nil {
		return nil, err
	}
	sharedClient = c
	return c, nil
}

func resetClient() {
	clientMu.Lock()
	if sharedClient != nil {
		sharedClient.Close()
		sharedClient = nil
	}
	clientMu.Unlock()
}

// withClient runs fn with the shared client, reconnecting once if the failure
// was a connection-level (transport) error.
func withClient(fn func(*Client) error) error {
	c, err := getClient()
	if err != nil {
		return err
	}
	err = fn(c)
	var te *transportError
	if err != nil && errors.As(err, &te) {
		resetClient()
		c, err2 := getClient()
		if err2 != nil {
			return err2
		}
		return fn(c)
	}
	return err
}

// Shutdown stops a running host (if any). Used by `cs reset`.
func Shutdown() error {
	hi, err := loadHostInfoForAuth()
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil // not running
		}
		return err
	}
	c, err := connectAndHello(hi, 500*time.Millisecond)
	if err != nil {
		return err
	}
	defer c.Close()
	if err := c.Shutdown(); err != nil {
		return err
	}
	resetClient()
	return nil
}

// HostInfo returns a human-readable summary of the native session host for
// `cs debug`: its pipe, PID, protocol version, and live sessions. It is robust
// to a stopped or version-skewed host (it reports status rather than failing).
func HostInfo() (string, error) {
	infoPath, err := hostInfoPath()
	if err != nil {
		return "", err
	}
	var b strings.Builder
	b.WriteString("Session host (native Windows):\n")
	fmt.Fprintf(&b, "  state file:  %s\n", infoPath)
	fmt.Fprintf(&b, "  client ver:  %d\n", proto.Version)

	hi, err := readHostInfoFile()
	if err != nil {
		if os.IsNotExist(err) {
			b.WriteString("  status:      not running (no state file)\n")
			return b.String(), nil
		}
		return "", err
	}
	fmt.Fprintf(&b, "  pipe:        %s\n", hi.PipeName)
	fmt.Fprintf(&b, "  pid:         %d\n", hi.PID)
	fmt.Fprintf(&b, "  host ver:    %d\n", hi.Version)
	fmt.Fprintf(&b, "  created:     %s\n", time.Unix(hi.CreatedUnix, 0).Format(time.RFC3339))

	expectedPipe, err := controlPipeName()
	if err != nil {
		return "", err
	}
	if err := validateHostInfoForAuth(hi, expectedPipe, processCreationUnix); err != nil {
		fmt.Fprintf(&b, "  status:      untrusted state file (%v)\n", err)
		return b.String(), nil
	}
	c, err := connectAndHello(hi, 800*time.Millisecond)
	if err != nil {
		fmt.Fprintf(&b, "  status:      authentication failed or host not reachable (%v)\n", err)
		return b.String(), nil
	}
	defer c.Close()
	sessions, err := c.ListSessions()
	if err != nil {
		fmt.Fprintf(&b, "  status:      reachable; ListSessions failed: %v\n", err)
		return b.String(), nil
	}
	fmt.Fprintf(&b, "  status:      reachable\n")
	fmt.Fprintf(&b, "  sessions:    %d\n", len(sessions))
	for _, s := range sessions {
		fmt.Fprintf(&b, "    - %-24s program=%s alive=%v exit=%d\n", s.Name, s.Program, s.Alive, s.ExitCode)
	}
	return b.String(), nil
}

var nonAlnum = regexp.MustCompile(`[^A-Za-z0-9]+`)

func sanitizeSessionName(title string) string {
	return "cs_" + strings.Trim(nonAlnum.ReplaceAllString(title, "_"), "_")
}

// Session is the Windows TerminalSession implementation. It is a thin client
// handle to a session owned by the session host, so it survives TUI restarts.
type Session struct {
	name    string
	program string

	mu   sync.Mutex
	cols int
	rows int
}

// NewSession creates a TerminalSession handle for the given instance title and
// program. The underlying session lives in the host (created on Start).
func NewSession(title, program string) *Session {
	return &Session{name: sanitizeSessionName(title), program: program, cols: 80, rows: 24}
}

func (s *Session) size() (int, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cols, s.rows
}

// Start creates the session in the host, running the program in workDir.
func (s *Session) Start(workDir string) error {
	cols, rows := s.size()
	return withClient(func(c *Client) error {
		return c.CreateSession(s.name, s.program, workDir, cols, rows, false)
	})
}

// Restore reconnects to an existing host session. If the host no longer has the
// session (e.g. the host died), it returns ErrSessionGone so the caller can
// recreate it.
func (s *Session) Restore() error {
	var exists, alive bool
	err := withClient(func(c *Client) error {
		e, a, err := c.HasSession(s.name)
		exists, alive = e, a
		return err
	})
	if err != nil {
		return err
	}
	if !exists || !alive {
		return ErrSessionGone
	}
	return nil
}

func (s *Session) Close() error {
	return withClient(func(c *Client) error { return c.Kill(s.name) })
}

func (s *Session) CapturePaneContent() (string, error) {
	var out string
	err := withClient(func(c *Client) error {
		v, err := c.CapturePane(s.name, proto.CaptureScreen, true)
		out = v
		return err
	})
	return out, err
}

func (s *Session) CapturePaneContentWithOptions(start, end string) (string, error) {
	var out string
	err := withClient(func(c *Client) error {
		v, err := c.CapturePane(s.name, proto.CaptureFull, true)
		out = v
		return err
	})
	return out, err
}

// capturePlain returns the plain (non-ANSI) screen for prompt matching.
func (s *Session) capturePlain() (string, error) {
	var out string
	err := withClient(func(c *Client) error {
		v, err := c.CapturePane(s.name, proto.CaptureScreen, false)
		out = v
		return err
	})
	return out, err
}

func (s *Session) HasUpdated() (updated bool, hasPrompt bool) {
	_ = withClient(func(c *Client) error {
		u, p, err := c.HasUpdated(s.name)
		updated, hasPrompt = u, p
		return err
	})
	return updated, hasPrompt
}

// TapEnter is intentionally a no-op on the native-Windows host model. AutoYes
// auto-Enter is owned by the session host (see conptySession.autoYesLoop), which
// also pauses while a client is attached. If the TUI/daemon also tapped Enter
// here we'd double-approve. SendKeys (e.g. for trust prompts) is unaffected.
func (s *Session) TapEnter() error { return nil }

func (s *Session) TryAutoApprove(sessionID string) bool { return false }

// SetAutoYes enables/disables host-side AutoYes for this session.
func (s *Session) SetAutoYes(enabled bool) error {
	return withClient(func(c *Client) error { return c.SetAutoYes(s.name, enabled) })
}

func (s *Session) SendKeys(keys string) error {
	return withClient(func(c *Client) error { return c.SendKeys(s.name, []byte(keys)) })
}

func (s *Session) SetDetachedSize(width, height int) error {
	s.mu.Lock()
	s.cols, s.rows = width, height
	s.mu.Unlock()
	return withClient(func(c *Client) error { return c.Resize(s.name, width, height) })
}

func (s *Session) DoesSessionExist() bool {
	var exists bool
	_ = withClient(func(c *Client) error {
		e, _, err := c.HasSession(s.name)
		exists = e
		return err
	})
	return exists
}

// DetachSafely defines what "pause" means on the native-Windows host model.
// Unlike tmux (where a paused session keeps running in the background and is
// re-attached on resume), a ConPTY session is bound to its worktree directory,
// which Pause removes. Leaving it alive would orphan a process in a deleted
// directory, so on Windows we kill the host session here; Resume then starts a
// fresh session in the recreated worktree (DoesSessionExist reports false).
func (s *Session) DetachSafely() error {
	err := withClient(func(c *Client) error { return c.Kill(s.name) })
	// A session that is already gone is fine — the goal is "not running".
	if err != nil && strings.Contains(err.Error(), "no such session") {
		return nil
	}
	return err
}

// CheckAndHandleTrustPrompt dismisses only prompts classified as safe. Folder
// trust and MCP trust prompts are intentionally not cleared here.
func (s *Session) CheckAndHandleTrustPrompt() bool {
	content, err := s.capturePlain()
	if err != nil {
		return false
	}
	match, ok := promptpolicy.Classify(s.program, content)
	if !ok || !promptpolicy.AllowsAutoApprove(match) {
		return false
	}
	if err := s.SendKeys(string(match.ApproveKeys)); err != nil {
		return false
	}
	promptpolicy.AuditAutoApproval(s.name, s.program, match, "startup-policy")
	return true
}
