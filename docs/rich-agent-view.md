# Rich agent view (experimental)

The **rich agent view** renders a GitHub Copilot CLI session as a structured chat
transcript — streaming assistant text, reasoning, tool cards, MCP server status, and
inline permission / `ask_user` prompts — driven by the official
[`github/copilot-sdk`](https://github.com/github/copilot-sdk) Go SDK, instead of
screen‑scraping a terminal.

It is **additive, opt‑in, and Copilot‑only**. The default ConPTY/tmux terminal backend
is unchanged and remains the default for every agent (and every non‑Copilot agent).

> **Status:** experimental, **Windows‑first** (it builds on the native Windows session
> host). The terminal backend is what ships enabled.

## Enabling it

Two independent gates, either of which opts a **Copilot** workspace into the rich view:

- **Per workspace (recommended):** tick **Rich agent view (experimental)** in the
  desktop **New workspace** dialog. The toggle only appears when the agent is Copilot.
- **Globally:** set `"copilot_rich_view": true` in `~/.hangar/config.json`.

The daemon makes the final decision in `richBackend(reqRich, cfgEnabled, copilotAgent)`
— the backend is rich only when (the request **or** the config opted in) **and** the
agent is Copilot. Anything else falls back to the terminal backend.

## Requirements

- Windows (the rich backend lives in the native Windows session host, `session/winhost/`).
- The Copilot CLI resolvable as `copilot` on `PATH` (or via `COPILOT_CLI_PATH`). The SDK
  launches `copilot --headless --stdio`.
- Auth, model access, instructions, and MCP servers come from the Copilot CLI + the
  SDK, **not** from your interactive shell. MCP servers are forwarded per session from
  `~/.copilot/mcp-config.json` (HTTP and stdio; each server is sent with `Tools:["*"]`).

## What works

- Streaming transcript: assistant messages (with token‑level deltas), reasoning,
  running/done tool cards (incl. the MCP server for MCP tools), title, usage, errors.
- **MCP server status** panel (per‑server `connected` / `failed` / `needs-auth` / …).
- **Interactivity:** send a message, **Stop** (abort the turn), answer **permission**
  prompts (Approve / Reject, or auto‑approved when AutoYes is on) and **`ask_user`**
  prompts (choices + freeform).
- **Persistence:** the transcript survives desktop restarts (replayed from the daemon)
  and **daemon restarts** — a rich session is resumed on demand from its persisted SDK
  session id when its stream is reopened (`Resume` + `Transcript()` replay).
- **Crash handling:** if the Copilot child process dies, the session is marked not‑alive
  and the next stream open revives it.

## MCP servers (Hangar catalog)

Beyond the Copilot CLI's own `~/.copilot/mcp-config.json`, Hangar keeps a **global MCP
catalog** with **per‑repo enablement** at **`~/.hangar/mcp.json`** (`config.GetConfigDir()`).
It applies to the **rich backend only** — terminal sessions are unaffected.

```jsonc
{
  "servers": {
    "my-stdio": { "type": "local", "command": "agency.exe", "args": ["serve"],
                  "env": { "TOKEN": "…" }, "cwd": "C:/tools", "tools": ["*"], "timeout": 60 },
    "my-http":  { "type": "http",  "url": "https://example/mcp",
                  "headers": { "Authorization": "Bearer …" } }
  },
  "repoEnabled": { "c:/users/me/code/myrepo": ["my-stdio"] }
}
```

- **Per‑repo scope:** a server is forwarded to a session only if its name is listed under
  the session repo's key in `repoEnabled`. Keys are canonicalized with
  **`canonRepoKey(p) = ToLower(ToSlash(Clean(p)))`** so the various stored repo‑path forms
  (forward‑slash from `git rev-parse`, back‑slash from `filepath.Clean`) reconcile to one key.
- **Types:** `type` is `"local"` (stdio) or `"http"`; **`"sse"` is accepted and treated as
  `http`**. If `type` is omitted it's inferred (`url` ⇒ http, `command` ⇒ stdio). `tools`
  defaults to `["*"]`; `timeout` is in **seconds** (`0` = unset, clamped to `0…600`); stdio
  `cwd` maps to the SDK's working directory.
- **Precedence — catalog wins:** the catalog overlay is applied **after** the CLI
  `mcp-config.json`, so on a name collision the Hangar catalog entry **overrides** the CLI
  one. The merged set is re‑read fresh on every session **Start/Resume**.
- **Windows command note:** stdio `command` is launched directly (no shell), so give the
  executable's real name **including extension** (e.g. `agency.exe`, `npx.cmd`); bare names
  relying on `PATHEXT`/shell resolution may not start.

Code map: `session/winhost/mcpcatalog_windows.go` (catalog load + `canonRepoKey` +
per‑repo resolution), threaded into session start via `sdkSessionParams.extraMCP` and
overlaid in `copilotsdk.Session.forwardedMCPServers()`.

## Architecture (how it fits)

- The rich backend is a **sibling** to the ConPTY terminal backend behind the host's
  `managedSession` interface — an **adapter** (`session/winhost/sdksession_windows.go`)
  wraps a `copilotsdk.Session` so it lives in the same host registry without touching the
  terminal path or the TUI. Terminal‑shaped methods (capture/sendKeys/resize) are no‑ops.
- One `copilot.Client` (and one Copilot child process) **per session**, so a crash in one
  rich session cannot take down the others.
- The daemon translates SDK events into a length‑prefixed JSON **event stream** and
  exposes **control** methods over the existing named‑pipe protocol (proto **v11**:
  `OpenRichStream` / `SendMessage` / `AbortTurn` / `GetTranscript`; proto **v12**:
  `RespondPermission` / `RespondUserInput`). The desktop renders it in
  `TranscriptView`; `CenterPane` picks rich vs terminal by `WorkspaceInfo.kind`.
- **Permissions** are answered out‑of‑band by `requestId`
  (`session.RPC.Permissions.HandlePendingPermissionRequest`) so the handler never blocks
  the shared runtime; **`ask_user`** has no out‑of‑band resolve, so its handler blocks on
  its own goroutine until the desktop answers.

Code map: `session/copilotsdk/` (SDK wrapper, MCP forwarding, permission/ask_user
policy), `session/winhost/sdksession_windows.go` + `richstream_windows.go` (adapter +
event pipeline), `session/winhost/proto/` (the wire contract),
`desktop/src/renderer/src/components/TranscriptView.tsx` (the UI).

## Limitations & notes

- Windows‑only and Copilot‑only; the terminal backend stays the default everywhere else.
- Unlike the terminal backend, the rich view does **not** inherit your interactive
  shell's environment/tools — it relies on the Copilot CLI's own auth/config plus the
  forwarded MCP config (the CLI's `mcp-config.json` **and** the Hangar catalog above).
- Distribution of the Copilot CLI (PATH vs bundling vs `COPILOT_CLI_PATH`) and the
  associated AGPL/licensing considerations are an open project decision.
- Some hardening items (hard‑crash orphan reconcile, a ~10‑session load test, a
  `runRichStream` end‑to‑end test) are pending and need an integration environment.
