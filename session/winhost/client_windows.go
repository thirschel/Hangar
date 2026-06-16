//go:build windows

package winhost

import (
	"errors"
	"fmt"
	"net"
	"sync"
	"time"

	"claude-squad/session/winhost/proto"

	"github.com/Microsoft/go-winio"
)

// VersionMismatch is returned by EnsureHost when a running host speaks a
// different protocol version. The caller (TUI) decides whether to restart it,
// since a restart destroys live sessions.
type VersionMismatch struct{ HostVersion, ClientVersion int }

func (e *VersionMismatch) Error() string {
	return fmt.Sprintf("session-host protocol mismatch: host=%d client=%d", e.HostVersion, e.ClientVersion)
}

// Client is a control-plane connection to the session host. It serializes
// request/response on a single connection.
type Client struct {
	conn   net.Conn
	mu     sync.Mutex
	nextID uint64
}

func dialClient(pipe string, timeout time.Duration) (*Client, error) {
	conn, err := winio.DialPipe(pipe, &timeout)
	if err != nil {
		return nil, err
	}
	return &Client{conn: conn}, nil
}

func (c *Client) call(req *proto.Request) (*proto.Response, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.nextID++
	req.ID = c.nextID
	_ = c.conn.SetWriteDeadline(time.Now().Add(30 * time.Second))
	if err := proto.WriteFrame(c.conn, req); err != nil {
		return nil, err
	}
	_ = c.conn.SetWriteDeadline(time.Time{})
	_ = c.conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	resp, err := proto.ReadResponse(c.conn)
	_ = c.conn.SetReadDeadline(time.Time{})
	if err != nil {
		return nil, err
	}
	if resp.ID != req.ID {
		return nil, fmt.Errorf("response id mismatch: got %d want %d", resp.ID, req.ID)
	}
	return resp, nil
}

// respErr converts a transport error or an unsuccessful response into a Go error.
func respErr(r *proto.Response, err error) error {
	if err != nil {
		return err
	}
	if !r.OK {
		return errors.New(r.Error)
	}
	return nil
}

// Close closes the control connection (does not stop the host).
func (c *Client) Close() error { return c.conn.Close() }

func (c *Client) Hello() (*proto.Response, error) {
	return c.call(&proto.Request{Method: proto.MethodHello, ClientVersion: proto.Version})
}

func (c *Client) CreateSession(name, program, workDir string, cols, rows int, autoYes bool) error {
	return respErr(c.call(&proto.Request{
		Method: proto.MethodCreateSession, Session: name, Program: program,
		WorkDir: workDir, Cols: cols, Rows: rows, AutoYes: autoYes,
	}))
}

func (c *Client) HasSession(name string) (exists, alive bool, err error) {
	r, e := c.call(&proto.Request{Method: proto.MethodHasSession, Session: name})
	if e != nil {
		return false, false, e
	}
	if !r.OK {
		return false, false, errors.New(r.Error)
	}
	return r.Exists, r.Alive, nil
}

func (c *Client) ListSessions() ([]proto.SessionInfo, error) {
	r, e := c.call(&proto.Request{Method: proto.MethodListSessions})
	if e != nil {
		return nil, e
	}
	if !r.OK {
		return nil, errors.New(r.Error)
	}
	return r.Sessions, nil
}

func (c *Client) CapturePane(name, mode string, withANSI bool) (string, error) {
	r, e := c.call(&proto.Request{Method: proto.MethodCapturePane, Session: name, Mode: mode, WithANSI: withANSI})
	if e != nil {
		return "", e
	}
	if !r.OK {
		return "", errors.New(r.Error)
	}
	return r.Content, nil
}

func (c *Client) SendKeys(name string, data []byte) error {
	return respErr(c.call(&proto.Request{Method: proto.MethodSendKeys, Session: name, Data: data}))
}

func (c *Client) Resize(name string, cols, rows int) error {
	return respErr(c.call(&proto.Request{Method: proto.MethodResize, Session: name, Cols: cols, Rows: rows}))
}

func (c *Client) HasUpdated(name string) (updated, hasPrompt bool, err error) {
	r, e := c.call(&proto.Request{Method: proto.MethodHasUpdated, Session: name})
	if e != nil {
		return false, false, e
	}
	if !r.OK {
		return false, false, errors.New(r.Error)
	}
	return r.Updated, r.HasPrompt, nil
}

func (c *Client) SetAutoYes(name string, enabled bool) error {
	return respErr(c.call(&proto.Request{Method: proto.MethodSetAutoYes, Session: name, Enabled: enabled}))
}

func (c *Client) Kill(name string) error {
	return respErr(c.call(&proto.Request{Method: proto.MethodKillSession, Session: name}))
}

// Shutdown asks the host to stop. The host stops after replying.
func (c *Client) Shutdown() error {
	return respErr(c.call(&proto.Request{Method: proto.MethodShutdown}))
}

// connectAndHello dials the pipe and validates the protocol version.
func connectAndHello(pipe string, timeout time.Duration) (*Client, error) {
	c, err := dialClient(pipe, timeout)
	if err != nil {
		return nil, err
	}
	r, err := c.Hello()
	if err != nil {
		c.Close()
		return nil, err
	}
	if !r.OK {
		c.Close()
		return nil, errors.New(r.Error)
	}
	if r.HostVersion != proto.Version {
		c.Close()
		return nil, &VersionMismatch{HostVersion: r.HostVersion, ClientVersion: proto.Version}
	}
	return c, nil
}

// EnsureHost connects to the running session host, spawning a detached one if
// none is running. The returned Client is a live control connection.
func EnsureHost() (*Client, error) {
	pipe, err := controlPipeName()
	if err != nil {
		return nil, err
	}
	if c, err := connectAndHello(pipe, 500*time.Millisecond); err == nil {
		return c, nil
	} else if vm := (*VersionMismatch)(nil); errors.As(err, &vm) {
		return nil, vm
	}
	if err := spawnDetachedHost(); err != nil {
		return nil, fmt.Errorf("spawn session-host: %w", err)
	}
	deadline := time.Now().Add(10 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		c, err := connectAndHello(pipe, 300*time.Millisecond)
		if err == nil {
			return c, nil
		}
		if vm := (*VersionMismatch)(nil); errors.As(err, &vm) {
			return nil, vm
		}
		lastErr = err
		time.Sleep(150 * time.Millisecond)
	}
	return nil, fmt.Errorf("session-host did not become ready: %w", lastErr)
}
