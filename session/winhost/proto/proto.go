// Package proto defines the wire protocol between the claude-squad TUI (client)
// and the native-Windows session-host daemon. It is intentionally
// platform-neutral (no Windows imports) so it can be unit-tested anywhere and
// shared by both the client and the host.
//
// Transport is a go-winio named pipe in byte mode; this package only defines the
// framing (length-prefixed JSON) and the request/response envelopes.
package proto

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
)

// Version is the protocol version. The client and host exchange it in the Hello
// handshake; a mismatch means the host must be restarted (see plan §6.7).
// v2 adds the workspace methods (the desktop "core daemon" surface).
// v3 adds per-workspace run process control and output polling.
// v4 adds agent-generated workspace titles (GenerateWorkspaceTitle).
// v5 adds Regenerate/ForceRegenerate (kill the current agent and start a fresh
// one in the same worktree, optionally seeded from a HANDOFF.md) plus additive
// WorkspaceInfo regenerate-status fields.
const Version = 5

// MaxFrameSize bounds a single JSON frame. CapturePane(full) can include the
// whole scrollback, so this is generous but still guards against abuse/OOM.
const MaxFrameSize = 16 << 20 // 16 MiB

// Method names for Request.Method.
const (
	MethodHello         = "Hello"
	MethodCreateSession = "CreateSession"
	MethodHasSession    = "HasSession"
	MethodListSessions  = "ListSessions"
	MethodCapturePane   = "CapturePane"
	MethodSendKeys      = "SendKeys"
	MethodResize        = "Resize"
	MethodHasUpdated    = "HasUpdated"
	MethodSetAutoYes    = "SetAutoYes"
	MethodKillSession   = "KillSession"
	MethodAttach        = "Attach"
	MethodShutdown      = "Shutdown"

	// Workspace methods (v2): the desktop core-daemon surface. A workspace is a
	// git worktree + branch + an agent terminal session, owned by the host.
	MethodListWorkspaces      = "ListWorkspaces"
	MethodCreateWorkspace     = "CreateWorkspace"
	MethodGetWorkspace        = "GetWorkspace"
	MethodArchiveWorkspace    = "ArchiveWorkspace"
	MethodWorkspaceDiff       = "WorkspaceDiff"
	MethodWorkspaceCommit     = "WorkspaceCommit"
	MethodWorkspacePush       = "WorkspacePush"
	MethodSetWorkspaceAutoYes = "SetWorkspaceAutoYes"
	MethodStartRun            = "StartRun"
	MethodStopRun             = "StopRun"
	MethodWorkspaceRunOutput  = "WorkspaceRunOutput"

	// Title generation (v4): summarize the first message into a workspace title.
	MethodGenerateWorkspaceTitle = "GenerateWorkspaceTitle"

	// Regenerate (v5): kill the current agent session and start a fresh one in the
	// same worktree/branch, optionally seeding it from an agent-written HANDOFF.md.
	MethodRegenerateAgent = "RegenerateAgent"
	MethodForceRegenerate = "ForceRegenerate"
)

// Capture modes for MethodCapturePane.
const (
	CaptureScreen = "screen" // just the visible screen
	CaptureFull   = "full"   // visible screen + scrollback history
)

// Request is the single envelope sent from client to host. Optional fields are
// interpreted per Method.
type Request struct {
	ID      uint64 `json:"id"`
	Method  string `json:"method"`
	Session string `json:"session,omitempty"`

	// CreateSession
	Program string `json:"program,omitempty"`
	WorkDir string `json:"workDir,omitempty"`
	Cols    int    `json:"cols,omitempty"`
	Rows    int    `json:"rows,omitempty"`
	AutoYes bool   `json:"autoYes,omitempty"`

	// SetAutoYes
	Enabled bool `json:"enabled,omitempty"`

	// SendKeys
	Data []byte `json:"data,omitempty"`

	// CapturePane
	Mode     string `json:"mode,omitempty"`
	WithANSI bool   `json:"withANSI,omitempty"`

	// Hello
	ClientVersion int `json:"clientVersion,omitempty"`

	// Workspace methods (v2)
	RepoPath    string `json:"repoPath,omitempty"`
	Title       string `json:"title,omitempty"`
	BaseBranch  string `json:"baseBranch,omitempty"`
	WorkspaceID string `json:"workspaceId,omitempty"`
	File        string `json:"file,omitempty"`
	Message     string `json:"message,omitempty"`
	Command     string `json:"command,omitempty"`
	SinceOffset int64  `json:"sinceOffset,omitempty"`

	// Regenerate (v5): when true the regenerate first asks the live agent to write
	// HANDOFF.md and seeds the fresh agent with it. Reuses Cols/Rows for the PTY.
	Handoff bool `json:"handoff,omitempty"`

	// ArchiveWorkspace: when true, also delete the worktree directory and
	// its branch; when false (default), keep the worktree and branch on disk.
	DeleteWorktree bool `json:"deleteWorktree,omitempty"`

	// Shell selects the shell used to launch the agent: "cmd", "powershell", "pwsh".
	// Empty falls back to the config default_shell, which itself defaults to "cmd".
	Shell string `json:"shell,omitempty"`
}

// SessionInfo is returned by ListSessions.
type SessionInfo struct {
	Name     string `json:"name"`
	Alive    bool   `json:"alive"`
	ExitCode int    `json:"exitCode"`
	Program  string `json:"program"`
}

// WorkspaceInfo describes a workspace (git worktree + branch + agent session).
type WorkspaceInfo struct {
	ID           string `json:"id"`
	Title        string `json:"title"`
	Program      string `json:"program"`
	RepoPath     string `json:"repoPath"`
	WorktreePath string `json:"worktreePath"`
	Branch       string `json:"branch"`
	SessionName  string `json:"sessionName"`
	Alive        bool   `json:"alive"`
	AutoYes      bool   `json:"autoYes"`
	Added        int    `json:"added"`
	Removed      int    `json:"removed"`
	CreatedUnix  int64  `json:"createdUnix"`
	RunCommand   string `json:"runCommand"`
	Running      bool   `json:"running"`
	PreviewURL   string `json:"previewUrl"`
	Busy         bool   `json:"busy"`    // agent is actively producing output
	Waiting      bool   `json:"waiting"` // agent is at a prompt awaiting input
	// Regenerate (v5): a regenerate is in progress for this workspace and its
	// current phase ("" | "handoff" | "restarting" | "seeding").
	Regenerating bool   `json:"regenerating"`
	RegenPhase   string `json:"regenPhase,omitempty"`
}

// FileDiffInfo is a per-file change summary in a WorkspaceDiff response.
type FileDiffInfo struct {
	Path    string `json:"path"`
	Added   int    `json:"added"`
	Removed int    `json:"removed"`
}

// Response is the single envelope sent from host to client.
type Response struct {
	ID    uint64 `json:"id"`
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`

	// Hello
	HostVersion int `json:"hostVersion,omitempty"`

	// CapturePane
	Content string `json:"content,omitempty"`

	// HasSession
	Exists bool `json:"exists,omitempty"`
	Alive  bool `json:"alive,omitempty"`

	// HasUpdated
	Updated   bool `json:"updated,omitempty"`
	HasPrompt bool `json:"hasPrompt,omitempty"`

	// ListSessions
	Sessions []SessionInfo `json:"sessions,omitempty"`

	// CreateSession / Attach handshake: the per-session attach pipe + one-time token.
	AttachPipe  string `json:"attachPipe,omitempty"`
	AttachToken string `json:"attachToken,omitempty"`

	// Workspace methods (v2)
	Workspaces []WorkspaceInfo `json:"workspaces,omitempty"`
	Workspace  *WorkspaceInfo  `json:"workspace,omitempty"`
	Files      []FileDiffInfo  `json:"files,omitempty"`
	Diff       string          `json:"diff,omitempty"`
	Data       []byte          `json:"data,omitempty"`
	NextOffset int64           `json:"nextOffset,omitempty"`
	RunRunning bool            `json:"runRunning,omitempty"`
	ExitCode   int             `json:"exitCode,omitempty"`
}

// Errorf builds a failed Response for the given request id.
func Errorf(id uint64, format string, args ...any) *Response {
	return &Response{ID: id, OK: false, Error: fmt.Sprintf(format, args...)}
}

// WriteFrame writes v as a length-prefixed JSON frame.
func WriteFrame(w io.Writer, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("marshal frame: %w", err)
	}
	return WriteRawFrame(w, b)
}

// WriteRawFrame writes raw bytes as a length-prefixed frame (no JSON encoding).
// Used for the attach handshake token and any raw payloads.
func WriteRawFrame(w io.Writer, b []byte) error {
	if len(b) > MaxFrameSize {
		return fmt.Errorf("frame too large: %d > %d", len(b), MaxFrameSize)
	}
	var hdr [4]byte
	binary.BigEndian.PutUint32(hdr[:], uint32(len(b)))
	if _, err := w.Write(hdr[:]); err != nil {
		return err
	}
	_, err := w.Write(b)
	return err
}

// ReadHeader reads the 4-byte length prefix and validates it against
// MaxFrameSize. Splitting header/body lets a server apply a read deadline to the
// body only, so it can keep a persistent control connection open while still
// bounding a half-sent frame.
func ReadHeader(r io.Reader) (uint32, error) {
	var hdr [4]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return 0, err
	}
	n := binary.BigEndian.Uint32(hdr[:])
	if n > MaxFrameSize {
		return 0, fmt.Errorf("frame too large: %d > %d", n, MaxFrameSize)
	}
	return n, nil
}

// ReadBody reads exactly n bytes (the frame payload).
func ReadBody(r io.Reader, n uint32) ([]byte, error) {
	buf := make([]byte, n)
	if _, err := io.ReadFull(r, buf); err != nil {
		return nil, err
	}
	return buf, nil
}

// DecodeRequest decodes a raw frame payload into a Request.
func DecodeRequest(b []byte) (*Request, error) {
	var req Request
	if err := json.Unmarshal(b, &req); err != nil {
		return nil, fmt.Errorf("decode request: %w", err)
	}
	return &req, nil
}

// ReadFrameBytes reads one length-prefixed frame and returns the raw JSON.
func ReadFrameBytes(r io.Reader) ([]byte, error) {
	n, err := ReadHeader(r)
	if err != nil {
		return nil, err
	}
	return ReadBody(r, n)
}

// ReadRequest reads and decodes a single Request frame.
func ReadRequest(r io.Reader) (*Request, error) {
	b, err := ReadFrameBytes(r)
	if err != nil {
		return nil, err
	}
	var req Request
	if err := json.Unmarshal(b, &req); err != nil {
		return nil, fmt.Errorf("decode request: %w", err)
	}
	return &req, nil
}

// ReadResponse reads and decodes a single Response frame.
func ReadResponse(r io.Reader) (*Response, error) {
	b, err := ReadFrameBytes(r)
	if err != nil {
		return nil, err
	}
	var resp Response
	if err := json.Unmarshal(b, &resp); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	return &resp, nil
}
