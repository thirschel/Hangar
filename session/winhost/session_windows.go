//go:build windows

package winhost

import (
	"errors"
	"regexp"
	"strings"
	"sync"
	"time"

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
	pipe, err := controlPipeName()
	if err != nil {
		return err
	}
	c, err := dialClient(pipe, 500*time.Millisecond)
	if err != nil {
		return nil // not running
	}
	defer c.Close()
	_ = c.Shutdown()
	resetClient()
	return nil
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

func (s *Session) TapEnter() error {
	return withClient(func(c *Client) error { return c.SendKeys(s.name, []byte{0x0d}) })
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

// DetachSafely is a no-op for the host model: the session lives in the host
// independent of any attach, so "detaching" (e.g. during Pause) leaves it
// running. Closing only the optional attach stream happens in P5.
func (s *Session) DetachSafely() error { return nil }

// CheckAndHandleTrustPrompt dismisses the one-time trust/confirmation prompt for
// supported agents. (P6 moves this fully host-side.)
func (s *Session) CheckAndHandleTrustPrompt() bool {
	content, err := s.capturePlain()
	if err != nil {
		return false
	}
	switch {
	case strings.Contains(s.program, "claude"):
		if strings.Contains(content, "Do you trust the files in this folder?") ||
			strings.Contains(content, "new MCP server") {
			_ = s.TapEnter()
			return true
		}
	case strings.Contains(s.program, "aider"), strings.Contains(s.program, "gemini"):
		if strings.Contains(content, "Open documentation url for more info") {
			_ = s.SendKeys("D\r")
			return true
		}
	}
	return false
}
