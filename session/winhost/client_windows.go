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

// transportError marks a connection-level failure (as opposed to a logical
// error returned by the host in Response.Error). The Session backend resets and
// reconnects on transportError but not on logical errors.
type transportError struct{ err error }

func (e *transportError) Error() string { return e.err.Error() }
func (e *transportError) Unwrap() error { return e.err }

func (c *Client) call(req *proto.Request) (*proto.Response, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.nextID++
	req.ID = c.nextID
	_ = c.conn.SetWriteDeadline(time.Now().Add(30 * time.Second))
	if err := proto.WriteFrame(c.conn, req); err != nil {
		return nil, &transportError{err}
	}
	_ = c.conn.SetWriteDeadline(time.Time{})
	_ = c.conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	resp, err := proto.ReadResponse(c.conn)
	_ = c.conn.SetReadDeadline(time.Time{})
	if err != nil {
		return nil, &transportError{err}
	}
	if resp.ID != req.ID {
		return nil, &transportError{fmt.Errorf("response id mismatch: got %d want %d", resp.ID, req.ID)}
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

// Attach requests an attach pipe for the session at the given console size,
// returning the pipe name and one-time auth token.
func (c *Client) Attach(name string, cols, rows int) (pipe, token string, err error) {
	r, e := c.call(&proto.Request{Method: proto.MethodAttach, Session: name, Cols: cols, Rows: rows})
	if e != nil {
		return "", "", e
	}
	if !r.OK {
		return "", "", errors.New(r.Error)
	}
	return r.AttachPipe, r.AttachToken, nil
}

// Shutdown asks the host to stop. The host stops after replying.
func (c *Client) Shutdown() error {
	return respErr(c.call(&proto.Request{Method: proto.MethodShutdown}))
}

// --- Workspace methods (v2) ---

func (c *Client) CreateWorkspace(repoPath, title, program, baseBranch string) (*proto.WorkspaceInfo, error) {
	r, e := c.call(&proto.Request{
		Method: proto.MethodCreateWorkspace, RepoPath: repoPath, Title: title,
		Program: program, BaseBranch: baseBranch,
	})
	if e != nil {
		return nil, e
	}
	if !r.OK {
		return nil, errors.New(r.Error)
	}
	return r.Workspace, nil
}

func (c *Client) ListWorkspaces() ([]proto.WorkspaceInfo, error) {
	r, e := c.call(&proto.Request{Method: proto.MethodListWorkspaces})
	if e != nil {
		return nil, e
	}
	if !r.OK {
		return nil, errors.New(r.Error)
	}
	return r.Workspaces, nil
}

func (c *Client) ArchiveWorkspace(id string) error {
	return respErr(c.call(&proto.Request{Method: proto.MethodArchiveWorkspace, WorkspaceID: id}))
}

func (c *Client) WorkspaceFiles(id string) ([]proto.FileDiffInfo, error) {
	r, e := c.call(&proto.Request{Method: proto.MethodWorkspaceDiff, WorkspaceID: id})
	if e != nil {
		return nil, e
	}
	if !r.OK {
		return nil, errors.New(r.Error)
	}
	return r.Files, nil
}

func (c *Client) WorkspaceFileDiff(id, file string) (string, error) {
	r, e := c.call(&proto.Request{Method: proto.MethodWorkspaceDiff, WorkspaceID: id, File: file})
	if e != nil {
		return "", e
	}
	if !r.OK {
		return "", errors.New(r.Error)
	}
	return r.Diff, nil
}

func (c *Client) CommitWorkspace(id, message string) error {
	return respErr(c.call(&proto.Request{Method: proto.MethodWorkspaceCommit, WorkspaceID: id, Message: message}))
}

func (c *Client) PushWorkspace(id string) error {
	return respErr(c.call(&proto.Request{Method: proto.MethodWorkspacePush, WorkspaceID: id}))
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
