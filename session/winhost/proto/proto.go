// Package proto defines the wire protocol between the hangar TUI (client)
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
// v6 adds UpdateWorkspace and the Copilot session browser methods.
// v7 makes Hello an authenticated nonce/HMAC challenge-response. Mixed old/new
// clients and hosts fail closed with a protocol mismatch before any command.
// v8 adds server-enforced cross-repo confirmation for ResumeCopilotSession
// (Request.Confirmed plus Response.NeedsConfirm/AbsPath). Bumping the version
// guarantees a new client cannot silently resume against an old host that would
// ignore the confirmation flag and create a worktree in an unconfirmed repo.
// v9 adds CaptureHistory (ANSI scrollback + terminal-mode replay for the desktop).
// CaptureHistory now also honors the request's Cols/Rows, rendering stored
// scrollback at the client's display width so it aligns with the fitted xterm
// grid (additive; clips/reveals rows, never reflows — no version change).
// v10 adds in-place (no-worktree) workspaces (Request.NoWorktree plus
// WorkspaceInfo.HasWorktree). Bumping the version guarantees a new client cannot
// silently request a no-worktree session against an old host that would ignore
// the flag and create a worktree in the selected folder's repo anyway.
// v11 adds the rich agent view: a structured event stream (OpenRichStream) plus
// SendMessage/AbortTurn/GetTranscript control for Copilot SDK-backed sessions.
// v12 adds permission/user-input answering on the rich stream (RespondPermission/
// RespondUserInput) so a detached / AutoYes-OFF session can be resolved by a client.
// v13 adds the rich MCP-detail + Skills snapshots on the event stream:
// EventKindMCPDetail carries the full per-server MCPServerInfo list (rebuilt on
// every MCP load / status change) and EventKindSkills carries the full SkillInfo
// list; each replaces the desktop's view wholesale. Additive frames — the
// per-server EventKindMCPStatus pill stream is unchanged.
// v14 adds the rich context-usage header + model selector. The (previously dead)
// EventKindUsage frame now carries Model/CurrentTokens/TokenLimit so the desktop
// can render context% (CurrentTokens/TokenLimit), and two request/response RPCs —
// ListModels (-> Response.Models) and SetModel (Request.Model = target id) —
// list/switch the active model on a rich session, scoped by Request.Session just
// like SendMessage. Additive: the usage fields, ModelInfo, and the two methods are
// all new surface; existing frames and methods are unchanged.
// v15 adds message file attachments: Request.Attachments carries absolute file
// paths the desktop sends alongside Request.Message on a SendMessage call, which
// the host maps to Copilot SDK file attachments. Additive: an omitted/empty
// Attachments sends a plain message exactly as before.
// v16 adds per-model reasoning effort + context tier to the model switch:
// ModelInfo.SupportedEfforts/DefaultEffort advertise a model's reasoning-effort
// options (from the SDK), and Request.Effort/Request.ContextTier ride along with
// Request.Model on a SetModel call so the host can pass them to the Copilot SDK
// SetModelOptions. Additive: an omitted/empty Effort and ContextTier switch the
// model exactly as before (SetModel with nil options).
// v17 enriches the rich tool stream so the desktop can render CLI-style tool lines
// (name + args + result). EventFrame gains ToolArgs — a concise arguments summary
// on EventKindToolStart — and ToolResult — a concise result/error summary on
// EventKindToolComplete. Both are short, single-line, and truncated; they never
// carry a full payload (e.g. a file's contents). Additive: a tool.start/tool.complete
// frame with empty summaries serializes exactly as it did under v16.
// v18 restores two rich-view selections across a session restart. EventFrame gains
// three new Kinds plus the fields they carry: permission.resolved (Decision =
// "approve"|"reject") and input.resolved (RequestID) translate the SDK permission/
// user-input COMPLETION events so an already-answered card is dismissed on resume
// instead of re-showing Approve/Reject; and model (Model + Effort + ContextTier)
// carries the session's active model so the desktop restores the model selector
// after a restart. Additive: the fields are omitempty and the Kinds are all new.
// v19 streams reasoning deltas to the rich view so the desktop can grow the
// "thinking" block live (as the CLI already does). EventFrame gains no fields: the
// new EventKindReasoningDelta ("assistant.reasoning.delta") carries each incremental
// chunk in the existing Text field, and the existing assistant.reasoning frame still
// delivers the complete block as the finalizer. Additive: a host that never emits the
// delta Kind serializes exactly as it did under v18.
// v20 correlates a permission request with the tool call it gates so the desktop can
// attach an AutoYes permission badge to the exact tool line. EventFrame gains
// ToolCallID — the SDK tool-call id, set on tool.start / tool.complete (from the
// ToolExecution*Data) and on permission.requested (the gated call's id, when the SDK
// provides one). Additive: omitempty drops it for frames/permissions without a call id.
// v21 surfaces the session's accumulated "AI units" used (the CLI's "AIC used") next to
// the context. EventFrame gains Aic — a float on the usage frame, the running sum of the
// SDK's AssistantUsageData CopilotUsage.TotalNanoAiu (in whole AI units). Additive:
// omitempty drops it on every non-usage frame and on a usage frame before any request.
// v22 timestamps turn completion. EventFrame gains Timestamp — the SDK event time
// (unix ms) set on the idle frame so the desktop can show when a turn completed.
// Additive: omitempty drops it on every frame that does not carry a time.
// v23 adds the rich Instructions snapshot: EventKindInstructions ("instructions")
// carries the full InstructionInfo list (the custom instructions the SDK loaded for
// the session, pulled via RPC.Instructions.GetSources) so the desktop can show an
// Instructions page alongside MCP servers / Skills. Additive: omitempty drops the
// new Instructions slice on every other frame.
const Version = 23

// MaxFrameSize bounds a single JSON frame. CapturePane(full) and CaptureHistory
// can include the whole scrollback, so this is generous but still guards against
// abuse/OOM.
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

	// UpdateWorkspace (v6): update mutable workspace fields (title, program, shell).
	MethodUpdateWorkspace = "UpdateWorkspace"

	// CaptureHistory (v6): expose emulator scrollback as ANSI for terminal priming.
	MethodCaptureHistory = "CaptureHistory"

	// Copilot session browser (v6): discover and resume local Copilot CLI sessions.
	MethodListCopilotSessions  = "ListCopilotSessions"
	MethodResumeCopilotSession = "ResumeCopilotSession"

	// Rich agent view (v11): structured event stream + control for SDK-backed
	// "rich" Copilot sessions (sibling to the byte-oriented terminal methods).
	MethodOpenRichStream = "OpenRichStream" // open the per-session structured event stream
	MethodSendMessage    = "SendMessage"    // send a user message to a rich session
	MethodAbortTurn      = "AbortTurn"      // interrupt the current turn
	MethodGetTranscript  = "GetTranscript"  // fetch the persisted transcript (for repaint)

	// Permission / user-input answering (v12): inbound control responses for the
	// rich event stream. A permission resolves out-of-band by RequestID; a user-input
	// answer unblocks the SDK handler that is waiting on RequestID.
	MethodRespondPermission = "RespondPermission" // approve/reject a pending permission.requested
	MethodRespondUserInput  = "RespondUserInput"  // answer a pending user_input.requested

	// Context-usage header + model selector (v14): list/switch the active model on a
	// rich session (scoped by Request.Session like SendMessage). The usage header
	// itself is delivered on the existing EventKindUsage event-stream frame, not a
	// method.
	MethodListModels = "ListModels" // -> Response.Models
	MethodSetModel   = "SetModel"   // Request.Model = target model id; live switch
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

	// CaptureHistory: when true, append the current rendered screen after scrollback.
	IncludeScreen bool `json:"includeScreen,omitempty"`

	// Hello
	ClientVersion int    `json:"clientVersion,omitempty"`
	ClientNonce   string `json:"clientNonce,omitempty"`

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

	// CreateWorkspace (v10): when true, open the session in-place against RepoPath
	// (the selected folder) WITHOUT creating a git worktree. Git features still
	// work when the folder is a repo; a non-repo folder opens with them disabled.
	NoWorktree bool `json:"noWorktree,omitempty"`

	// Rich (CreateWorkspace) requests the opt-in Copilot SDK "rich agent view"
	// backend for a Copilot agent. Server-gated by config copilot_rich_view; the
	// terminal backend is used unless both this/the config and a Copilot agent agree.
	Rich bool `json:"rich,omitempty"`

	// Shell selects the shell used to launch the agent: "cmd", "powershell", "pwsh".
	// Empty falls back to the config default_shell, which itself defaults to "cmd".
	Shell string `json:"shell,omitempty"`

	// ResumeCopilotSession
	SessionID string `json:"sessionId,omitempty"`

	// Confirmed (v8): set by the client to acknowledge a cross-repo resume after
	// the host returned NeedsConfirm. The host refuses to create a worktree in a
	// repo other than its own working directory unless this is true (F-03).
	Confirmed bool `json:"confirmed,omitempty"`

	// Rich agent view (v11). Since selects an event-stream/transcript replay point:
	// only frames with Seq > Since are returned/streamed. Message (above) carries the
	// SendMessage text.
	Since uint64 `json:"since,omitempty"`

	// Permission / user-input answering (v12). RequestID correlates with an
	// EventFrame.RequestID (permission.requested / user_input.requested). Decision is
	// DecisionApprove or DecisionReject for RespondPermission. Answer + Freeform carry
	// the RespondUserInput reply (Freeform = the answer was typed, not a listed choice).
	RequestID string `json:"requestId,omitempty"`
	Decision  string `json:"decision,omitempty"`
	Answer    string `json:"answer,omitempty"`
	Freeform  bool   `json:"freeform,omitempty"`

	// Model (v14) is the target model id for MethodSetModel (live model switch on a
	// rich session). Unused by every other method.
	Model string `json:"model,omitempty"`

	// Effort/ContextTier (v16) ride along with Model on a MethodSetModel call; with
	// both empty the switch behaves exactly as v14 (nil SDK SetModelOptions).
	Effort      string `json:"effort,omitempty"`      // reasoning effort; empty = leave unset
	ContextTier string `json:"contextTier,omitempty"` // "default"|"long_context"; empty = leave unset

	// Attachments (v15) carries absolute file paths the desktop sends alongside
	// Message on a MethodSendMessage call. Empty/nil = a plain message (unchanged).
	Attachments []string `json:"attachments,omitempty"` // absolute file paths sent with a message
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
	// LastOutputUnix is the last time the agent produced output that changed its
	// visible screen, in Unix seconds; 0 = unknown (no live session or it never
	// changed). Additive, optional response field — does not affect the version
	// handshake, so Version is intentionally not bumped for it.
	LastOutputUnix int64  `json:"lastOutputUnix"`
	RunCommand     string `json:"runCommand"`
	Running        bool   `json:"running"`
	PreviewURL     string `json:"previewUrl"`
	Busy           bool   `json:"busy"`    // agent is actively producing output
	Waiting        bool   `json:"waiting"` // agent is at a prompt awaiting input
	// Regenerate (v5): a regenerate is in progress for this workspace and its
	// current phase ("" | "handoff" | "restarting" | "seeding").
	Regenerating bool   `json:"regenerating"`
	RegenPhase   string `json:"regenPhase,omitempty"`
	Shell        string `json:"shell,omitempty"`
	// HasWorktree (v10) is true when the workspace is backed by a managed git
	// worktree, and false for an in-place session opened directly against
	// RepoPath. Drives the sidebar worktree icon and archive safety. Defaults to
	// true for workspaces persisted before v10 (they all had worktrees).
	HasWorktree bool `json:"hasWorktree"`
	// Kind is the session backend: "terminal" (ConPTY/tmux, the default) or "rich"
	// (the opt-in Copilot SDK structured view). Additive, optional response field —
	// does not affect the version handshake, so Version is intentionally not bumped
	// for it. Empty/absent means "terminal".
	Kind string `json:"kind,omitempty"`
}

// Workspace session backends (WorkspaceInfo.Kind).
const (
	WorkspaceKindTerminal = "terminal" // ConPTY (Windows) / tmux (Unix); the default
	WorkspaceKindRich     = "rich"     // the opt-in Copilot SDK "rich agent view"
)

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
	HostVersion     int    `json:"hostVersion,omitempty"`
	HostPID         int    `json:"hostPid,omitempty"`
	HostCreatedUnix int64  `json:"hostCreatedUnix,omitempty"`
	HostNonceProof  string `json:"hostNonceProof,omitempty"`

	// CapturePane
	Content string `json:"content,omitempty"`

	// CaptureHistory
	AltScreen       bool `json:"altScreen,omitempty"`
	ScrollbackLines int  `json:"scrollbackLines,omitempty"`

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

	// Copilot session browser (v6)
	CopilotSessions []CopilotSessionInfo `json:"copilotSessions,omitempty"`
	Skipped         int                  `json:"skipped,omitempty"`

	// Cross-repo resume confirmation (v8). When NeedsConfirm is true the host
	// declined to resume because the target repo differs from its own working
	// directory; AbsPath is the fully resolved repo path to show the user. The
	// client re-issues ResumeCopilotSession with Confirmed=true to proceed (F-03).
	NeedsConfirm bool   `json:"needsConfirm,omitempty"`
	AbsPath      string `json:"absPath,omitempty"`

	// Rich agent view (v11): GetTranscript / OpenRichStream replay frames.
	Frames []EventFrame `json:"frames,omitempty"`

	// Rich model selector (v14): the MethodListModels result.
	Models []ModelInfo `json:"models,omitempty"`
}

// EventFrame is one structured event on a rich session's event stream (v11). It is
// serialized as length-prefixed JSON exactly like every other frame (WriteFrame /
// ReadFrameBytes). Seq is monotonic per session so clients can dedupe and replay.
// Fields are interpreted per Kind; unused fields are omitted.
type EventFrame struct {
	Seq       uint64 `json:"seq"`
	Kind      string `json:"kind"`
	Text      string `json:"text,omitempty"`      // assistant.message / assistant.delta / assistant.reasoning(.delta) text
	ToolName  string `json:"toolName,omitempty"`  // tool.start / tool.complete
	MCPServer string `json:"mcpServer,omitempty"` // tool.* : MCP server name, when the tool is an MCP tool
	// SDK event time in unix ms (v22). Set on the idle frame so the desktop can show
	// when a turn completed. omitempty drops it on frames without a time.
	Timestamp int64 `json:"ts,omitempty"`
	// SDK tool-call id (v20). Set on tool.start / tool.complete (the executing call)
	// and on permission.requested (the gated call's id, when the SDK provides one),
	// so the desktop can attach an AutoYes permission badge to the exact tool line.
	ToolCallID string   `json:"toolCallId,omitempty"`
	RequestID  string   `json:"requestId,omitempty"` // permission.requested / user_input.requested id
	Question   string   `json:"question,omitempty"`  // user_input.requested prompt
	Choices    []string `json:"choices,omitempty"`   // user_input.requested choices
	Title      string   `json:"title,omitempty"`     // title : the new session title
	Status     string   `json:"status,omitempty"`    // mcp/status changes
	Aborted    bool     `json:"aborted,omitempty"`   // idle : the preceding turn was aborted
	Error      string   `json:"error,omitempty"`     // error : message
	// Concise tool summaries (v17): populated only on the tool stream so the desktop
	// can render CLI-style tool lines (name + args + result). Both are short,
	// single-line, and truncated — never a full payload.
	ToolArgs   string `json:"toolArgs,omitempty"`   // concise tool arguments summary (on EventKindToolStart)
	ToolResult string `json:"toolResult,omitempty"` // concise tool result/error summary (on EventKindToolComplete)
	// Context-usage header (v14): populated only on EventKindUsage. Model is the
	// session's active model; CurrentTokens/TokenLimit are the context-window usage
	// the desktop renders as context% (CurrentTokens/TokenLimit). Best-effort —
	// omitempty drops any value the SDK has not reported yet.
	Model         string `json:"model,omitempty"`         // active model (on "usage")
	CurrentTokens int    `json:"currentTokens,omitempty"` // context tokens used (on "usage")
	TokenLimit    int    `json:"tokenLimit,omitempty"`    // context window size (on "usage")
	// AI units consumed this session (v21): the running sum of AssistantUsageData
	// CopilotUsage.TotalNanoAiu (in whole AI units), surfaced as the CLI's "AIC used".
	// Populated only on EventKindUsage; omitempty drops it before any request.
	Aic float64 `json:"aic,omitempty"` // accumulated AI units used (on "usage")
	// MCP-detail + Skills snapshots (v13): each carries a full list that replaces
	// the desktop view wholesale, populated only on its corresponding Kind.
	MCPServers []MCPServerInfo `json:"mcpServers,omitempty"` // populated on EventKindMCPDetail
	Skills     []SkillInfo     `json:"skills,omitempty"`     // populated on EventKindSkills
	// Instructions snapshot (v23): the full list of custom instructions the SDK
	// loaded for the session, populated only on EventKindInstructions and replacing
	// the desktop's Instructions page wholesale.
	Instructions []InstructionInfo `json:"instructions,omitempty"` // populated on EventKindInstructions
	// Resume-restore fields (v18). Decision is "approve"|"reject" on a
	// permission.resolved frame (the SDK permission completion). Effort/ContextTier
	// ride with Model (above) on a model frame so the desktop restores the model
	// selector after a restart; all three are best-effort and omitempty.
	Decision    string `json:"decision,omitempty"`    // permission.resolved: "approve"|"reject"
	Effort      string `json:"effort,omitempty"`      // model: reasoning effort (with Model/ContextTier)
	ContextTier string `json:"contextTier,omitempty"` // model: pinned context tier (with Model/Effort)
}

// EventFrame.Kind values for the rich event stream (v11).
const (
	EventKindAssistantMessage  = "assistant.message"
	EventKindAssistantDelta    = "assistant.delta"
	EventKindReasoning         = "assistant.reasoning"
	EventKindReasoningDelta    = "assistant.reasoning.delta" // incremental reasoning chunk (v19); finalized by EventKindReasoning
	EventKindToolStart         = "tool.start"
	EventKindToolComplete      = "tool.complete"
	EventKindPermissionRequest = "permission.requested"
	EventKindUserInputRequest  = "user_input.requested"
	EventKindUsage             = "usage"
	EventKindTitle             = "title"
	EventKindIdle              = "idle"
	EventKindError             = "error"
	EventKindMCPStatus         = "mcp.status"   // per-server MCP connection status (MCPServer, Status, Error)
	EventKindMCPDetail         = "mcp.detail"   // full MCP server-list snapshot
	EventKindSkills            = "skills"       // full skills-list snapshot
	EventKindInstructions      = "instructions" // full custom-instructions snapshot (v23)
	// Resume-restore frames (v18): translate the SDK completion events and carry the
	// active model so a restarted session restores its answered cards and selection.
	EventKindPermissionResolved = "permission.resolved" // a permission request was answered (Decision)
	EventKindInputResolved      = "input.resolved"      // a user-input/elicitation request was answered (RequestID)
	EventKindModel              = "model"               // the session's active model (Model + Effort + ContextTier)
)

// MCPServerInfo is per-server detail for the rich MCP page (v13). It is display-
// safe: names/status/transport/source/tool-names only, never command args, env,
// URLs, headers, or tokens.
type MCPServerInfo struct {
	Name      string   `json:"name"`
	Status    string   `json:"status,omitempty"`    // connected|failed|needs-auth|pending|disabled|not_configured
	Transport string   `json:"transport,omitempty"` // stdio|http|sse|memory
	Source    string   `json:"source,omitempty"`    // user|workspace|plugin|builtin
	Error     string   `json:"error,omitempty"`
	Tools     []string `json:"tools,omitempty"` // tool names (best-effort; omit if unknown)
}

// SkillInfo is one skill for the rich Skills page (v13).
type SkillInfo struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Enabled     bool   `json:"enabled"`
	Source      string `json:"source,omitempty"` // project|personal-copilot|plugin|builtin
	Path        string `json:"path,omitempty"`
}

// InstructionInfo is one loaded custom-instruction source for the rich Instructions
// page (v23). It is display-safe: the label, on-disk path, category/location, an
// optional description, the applies-to glob patterns, and the raw file content the
// SDK reported via RPC.Instructions.GetSources.
type InstructionInfo struct {
	Label       string   `json:"label"`
	SourcePath  string   `json:"sourcePath,omitempty"`
	Type        string   `json:"type,omitempty"`     // category used for merge logic
	Location    string   `json:"location,omitempty"` // where the source lives (UI grouping)
	Description string   `json:"description,omitempty"`
	ApplyTo     []string `json:"applyTo,omitempty"` // frontmatter globs; applies only to matching files
	Content     string   `json:"content,omitempty"` // raw instruction file content
}

// ModelInfo is one selectable model for the rich model selector (v14), returned in
// Response.Models for MethodListModels. winhost maps the copilotsdk model list onto
// it; it is display-safe (id + name only).
type ModelInfo struct {
	ID   string `json:"id"`
	Name string `json:"name,omitempty"`

	// SupportedEfforts/DefaultEffort (v16) advertise a model's reasoning-effort
	// options so the desktop can offer a per-model effort picker on the model switch.
	SupportedEfforts []string `json:"supportedEfforts,omitempty"` // from SDK SupportedReasoningEfforts
	DefaultEffort    string   `json:"defaultEffort,omitempty"`    // from SDK DefaultReasoningEffort
}

// Decision values for MethodRespondPermission (v12).
const (
	DecisionApprove = "approve"
	DecisionReject  = "reject"
)

// CopilotSessionInfo describes a discovered local Copilot CLI session.
type CopilotSessionInfo struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Repository string `json:"repository"`
	Branch     string `json:"branch"`
	OriginRoot string `json:"originRoot"`
	CreatedAt  int64  `json:"createdAt"` // Unix seconds
	UpdatedAt  int64  `json:"updatedAt"` // Unix seconds
	InUse      bool   `json:"inUse"`
	FirstMsg   string `json:"firstMsg,omitempty"`
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
