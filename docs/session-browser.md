# Feature: Copilot Session Browser

> Status: Draft v2 — revised after multi-model review (GPT-5.5, Gemini 3.1 Pro, Claude Opus 4.7)
> Area: new `session/copilot/` (cross-platform), new `ui/sessionBrowser.go`, `app/app.go`, `keys/keys.go`, `ui/menu.go`, `session/instance.go`, `session/storage.go`, new `session/agentcmd.go`, `session/winhost/`
> Related current behavior: Hangar creates each workspace as a git **worktree** running an agent (`copilot`, `claude`, …). **Two parallel launch paths exist:** the **TUI `Instance` path** (`n`/`N` → `NewInstance` → `Start` → `NewTerminalSession(title, program)`) launches the program **raw** — no `--session-id`, so Copilot assigns a random id and there is no resume continuity across TUI restarts; the **daemon `workspaceManager` path** (`session/winhost/workspace_windows.go`) is the only one that today seeds a stable `--session-id`. Either way, every Copilot run is discoverable under `~/.copilot/session-state`, but there is currently **no way to browse, search, or resume past Copilot sessions** from the TUI. (Implementing §7.5 gives the TUI path resume-on-restart as a side benefit.)

## 0. Revision history & review resolutions

**v2** incorporates a three-model review. Resolved blocking issues and key decisions:

- **Flag semantics (resolved):** do **not** use one shared helper that always emits `--resume`. Split into two intents — **seed a new session** (`--session-id=<new-uuid>`) and **resume an existing session** (`--resume=<id>`). See §7.5 and OQ in §11. (GPT + Claude consensus; Gemini's simpler "`--session-id` for both" is documented as an accepted alternative.)
- **Branch safety on restart (resolved, was a data-loss bug):** the restart flow must **not** create a branch from the current `HEAD` by default. It creates a **new, uniquely-named** branch seeded from the session's **recorded** `headCommit` (immutable, from `events.jsonl` `session.start`), and **never** reuses/clobbers the session's original branch. See §7.4.
- **Origin-repo is frozen (resolved):** the "origin repo" used for the new worktree is snapshotted into the index from the **earliest** `session.start.data.context.gitRoot`, because a resumed Copilot likely rewrites `workspace.yaml`'s `cwd`/`git_root` to the new worktree path. See §4 / §7.1.
- **All launch sites covered (resolved):** persist `AgentSessionID` and apply the resume-aware command at **`Start()`, the paused branch of `FromInstanceData()`, and `Resume()`** — not just `Start()`. See §7.5.
- **Re-resume safety (resolved):** hard-block on a fresh `inuse.*.lock`, track already-resumed ids in-process, and add a uniqueness suffix to the title/branch so resuming the same session twice cannot collide. See §7.4 / §10.
- **`gopkg.in/yaml.v3` (resolved):** already an **indirect** dependency (`go.mod`); promote to direct (`go mod tidy`) or hand-parse the flat file. See §11.
- **Launch path (decided):** extend the **TUI `Instance` path** (not the daemon `workspaceManager`); all three reviewers concurred. See §7.5.

## 1. Summary

Add a full-screen **Session Browser** to the TUI that lets users **search/filter their existing GitHub Copilot CLI sessions** by keyword and **"restart"** (resume) any one of them as a new, isolated Hangar workspace.

Copilot persists every session as a folder under `~/.copilot/session-state/<uuid>/` (445 such folders on the reference machine). Each holds machine-readable metadata (`workspace.yaml`) and the full conversation transcript (`events.jsonl`). The browser:

1. **Discovers** all local Copilot sessions across every repository.
2. **Searches** them by keyword, matching **both metadata** (name, repo, branch, dates) **and conversation text** (user/assistant messages), using a lazy/cached index so typing stays responsive over hundreds of sessions.
3. **Previews** the selected session (title, repo, branch, last activity, a matching snippet).
4. **Restarts** the selected session: creates a fresh git worktree in **that session's original repository** and launches `copilot --resume=<session-id>`, continuing the **same** conversation with full history.

This turns a pile of opaque UUID folders into a searchable, resumable history, with Hangar's worktree isolation applied automatically.

## 2. Background & current state

### 2.1 Copilot session-state on disk (verified, 445 sessions on reference machine)

Each session is a directory `~/.copilot/session-state/<session-uuid>/`. On Windows this is `C:\Users\<user>\.copilot\session-state\`.

Relevant files (per-session):

- **`workspace.yaml`** — present in **100%** of sessions. Flat YAML. Example:
  ```yaml
  id: 2331de89-df3c-43cf-8ac0-2c8f0886b9a4
  cwd: D:\dev\Hanger
  git_root: D:\dev\Hanger
  repository: thirschel/Hangar
  host_type: github
  branch: desktop-core-daemon
  client_name: github/cli
  name: Plan Session Browser Feature
  user_named: false
  summary_count: 0
  created_at: 2026-06-16T21:15:55.071Z
  updated_at: 2026-06-16T21:25:56.277Z
  remote_steerable: false
  mc_task_id: 78f1cd7d-...
  mc_session_id: a1e4baef-...
  ```
  - `name` is an auto-generated or user-set title. **~67% of sessions (298/445) have no `name`** → the browser needs a display-name fallback.
  - `git_root` / `cwd` / `repository` / `branch` identify the originating workspace — the key inputs for "restart in original repo".
  - `created_at` / `updated_at` are RFC-3339 UTC.

- **`events.jsonl`** — present in **~86%** of sessions (384/445); the rest are empty/never-run. One JSON object per line. Observed `type` values include `session.start`, `user.message`, `assistant.message`, `assistant.turn_start/end`, `tool.execution_start/complete`, `hook.start/end`, `subagent.started`, `session.model_change`, `session.mode_changed`, `system.message`. Search-relevant shapes:
  - `session.start` → `data.context` = `{ cwd, gitRoot, branch, headCommit, baseCommit, repository, hostType, repositoryHost }`. **Immutable** (written once at session start) — this is the authoritative source for the session's **origin repo and original commit**, and is preferred over `workspace.yaml` for those fields because Copilot may later rewrite `workspace.yaml`'s `cwd`/`git_root` (see §4 origin-freeze).
  - `user.message` → `data.content` (raw user text) and `data.transformedContent` (augmented). Also `data.agentMode`, `data.timestamp`.
  - `assistant.message` → `data.content` (assistant text).
  - File sizes range from a few KB to ~350 KB; 384 files × up to ~350 KB ≈ **~150 MB** worst-case if naively scanned.

- **`session.db`** — SQLite, present in ~65% of sessions. Contains only the agent's scratch tables (`todos`, `todo_deps`, `inbox_entries`). **Not useful** for browsing/search.

- Other artifacts (ignored by the browser): `checkpoints/`, `files/`, `research/`, `rewind-snapshots/`, `vscode.metadata.json`, and `inuse.<pid>.lock` (present while a session is actively open — relevant for the "already in use" edge case).

Sessions span **multiple repositories** (e.g. on the reference machine: `thirschel/Nestworthly` ×309, `thirschel/FiCharts` ×78, `thirschel/Hangar` ×11, plus others).

### 2.2 Copilot resume mechanism (verified via `copilot --help`)

- `copilot --resume[=<value>]` — resume a previous session; `<value>` may be a session ID, task ID, **7+ char ID prefix**, or **exact (case-insensitive) name**. No value → interactive picker.
- `copilot --session-id=<id>` — resume an existing session by ID **or** set the UUID for a new session.
- `copilot --continue` — resume the most recent session.

The browser uses **`copilot --resume=<session-id>`** for resume, always passing the **full session UUID** (never a name or 7-char prefix, which can be ambiguous). For creating *new* Copilot sessions, `--session-id=<new-uuid>` is used instead (it seeds the id). These are two distinct intents handled by two helpers — see §7.5.

> **Data-integrity note:** `--resume` continues the **same** session id and writes back into the **same** `~/.copilot/session-state/<id>/` folder (this is the intended "continue the conversation" behavior). It does **not** fork into a new id. Two processes resuming the same id concurrently would both write that folder — see the in-use hard-block in §7.4 / §10.

### 2.3 Hangar architecture relevant to this feature (verified)

- **TUI model** — `app/app.go`, a Bubble Tea `home` struct with a `state` enum (`stateDefault`, `stateNew`, `statePrompt`, `stateHelp`, `stateConfirm`). Overlays are drawn over the main view via `overlay.PlaceOverlay(...)` in `View()`. Adding a new screen = new `state` value + a component + key routing + render branch.
- **Keys** — `keys/keys.go` (`KeyName` enum, `GlobalKeyStringsMap`, `GlobalkeyBindings`). Menu entries in `ui/menu.go` (`MenuState` + `updateOptions()`). The keys `b` and `S` are currently unbound.
- **List rendering** — `ui/list.go` (`List` owns `items []*session.Instance`, `selectedIdx`, and a `repos map[string]int`). Selection wraps via `Up()/Down()`. The list is **already multi-repo-aware** (renders repo when `len(repos) > 1`).
- **Instance lifecycle** — `session/instance.go`. `NewInstance(InstanceOptions{Title, Path, Program, …})` → `Start(true)` creates the worktree (`git.NewGitWorktree(i.Path, …)`) then launches the agent via `NewTerminalSession(i.Title, i.Program)`. An optional initial `Prompt` is sent post-start via `SendPrompt()` (types into the PTY + Enter). Persisted by `session/storage.go` (`InstanceData`; includes `Program`).
- **Git worktree** — `session/git/worktree.go`: `NewGitWorktree(repoPath, sessionName)` **already accepts an arbitrary repo path** and resolves its toplevel. Multi-repo restart is therefore feasible at the git layer with no new primitives.
- **Existing Copilot resume plumbing (Windows)** — `session/winhost/workspace_windows.go`:
  - `supportsResume(program)` → true when program contains `"copilot"`.
  - `agentLaunchCommand(program, sessionID)` → appends `--session-id=<id>` for copilot.
  - The `workspace` struct persists `AgentSessionID`; `create()` generates a fresh UUID for new sessions and `reviveBySession()` relaunches with it.
  - **Important:** this helper is used by the daemon-style `workspaceManager` path, **not** by the TUI `Instance` path, which currently passes `Program` to the host **raw** (`host_windows.go startManagedSession`). The feature must inject the resume flag into whichever path the browser uses (see §7).

## 3. Goals / Non-goals

### Goals
- A full-screen browser, opened by a single key from the default view, listing **all** local Copilot sessions regardless of repo.
- **Keyword search/filter** over metadata **and** conversation content, responsive over 100s of sessions.
- A readable **preview** of the highlighted session, with a robust display-name fallback.
- **Restart** = create a new Hangar workspace that resumes the chosen session via `copilot --resume=<id>`, with the worktree created in the **session's original repo** (`git_root`).
- Discovery/parsing/search implemented in a **cross-platform** package; Windows is the first wired-up target.

### Non-goals (initial)
- Editing, renaming, deleting, or exporting Copilot sessions.
- **Forking** a session into a new id (Copilot resume continues the same id; true fork isn't natively supported).
- Browsing non-Copilot agents (claude/aider/gemini don't expose an equivalent on-disk session store here).
- Remote/cloud sessions, watching the directory for live changes, or a full-text search engine dependency.
- Wiring discovery into macOS/Linux launch flows (layer is cross-platform; only Windows resume is wired now).

## 4. Terminology & definitions

- **Copilot session** — a `~/.copilot/session-state/<uuid>/` directory; identified by its `id`.
- **Session metadata** — fields parsed from `workspace.yaml` (with `events.jsonl` `session.start` as fallback).
- **Session content** — concatenated `user.message`/`assistant.message` text from `events.jsonl`.
- **Restart / resume** — launching `copilot --resume=<id>` in a **new, uniquely-named** worktree; continues the same conversation. Never reuses or deletes the session's original branch.
- **Display name** — `name` if present; else first user-message snippet; else `repo @ branch`; else short id.
- **Origin repo** — the local repo the session ran in. Taken from the **earliest** `session.start.data.context.gitRoot` (immutable), snapshotted into the index; `workspace.yaml` `git_root` is only a fallback because Copilot may overwrite it on resume (§4-note below).
- **Origin commit** — `session.start.data.context.headCommit`, the commit the original conversation was working from; used as the base for the new worktree's branch.

## 5. UX design

### 5.1 Opening & layout
- From `stateDefault`, pressing **`b`** ("browse sessions") enters a new full-screen `stateBrowse` (rendered in place of the normal list/preview, not as a small centered overlay).
- Layout (top → bottom):
  1. **Search bar** — single-line input, focused on open; placeholder "Search Copilot sessions…". Shows live result count ("37/445").
  2. **Results list** — scrollable; each row: status glyph (in-use/empty), **display name** (truncated), `repo` · `branch`, relative `updated_at` (e.g. "3h ago"), and a one-line **match snippet** when the match came from content.
  3. **Preview pane** (right side or bottom, mirroring the main view split) — selected session's full metadata + a transcript snippet around the match / first user message. The snippet is taken from the **in-memory** index haystack (or fetched via an async `tea.Cmd`) so rapid `↑/↓` navigation never blocks on a synchronous `events.jsonl` read.
  4. **Footer/menu** — context keybindings.

### 5.2 Search / filter behavior
- Typing filters **live** with **debounce** (~150 ms) so keystrokes never block on disk I/O.
- Matching: **case-insensitive**; whitespace splits the query into terms; a session matches when **all** terms appear somewhere in its searchable text (metadata + content) — i.e. AND semantics.
- Default **sort**: most recently updated first. Optional secondary sort by relevance (term hit count) when a query is present.
- Empty query → list all sessions (most-recent first).

### 5.3 Keybindings (proposed)
- `b` — open Session Browser (from default).
- Type — edit search query.
- `↑/↓` (and `Ctrl+k`/`Ctrl+j` while the text box has focus) — move selection.
- `Enter` — **restart** the selected session (opens a brief confirmation if its origin repo ≠ current repo, or if it's in-use).
- `Tab` — toggle focus between search box and preview (for scrolling long transcripts).
- `Esc` / `q` — close the browser, return to `stateDefault`.
- `Ctrl+r` — force re-scan / rebuild index (ignore cache).

### 5.4 Menu / help updates
- Add a `KeyBrowse` entry to the default menu (`ui/menu.go`) and a new `MenuState` for the browser footer (search/navigate/restart/back).
- Document the new screen + keys in `app/help.go` and the README.

### 5.5 Empty & edge states
- **No sessions found** (empty dir / none match) → centered hint ("No Copilot sessions found" / "No matches for '<query>'").
- **No `~/.copilot/session-state`** → friendly message that Copilot isn't installed / has no sessions; browser still opens.
- **Empty session** (no `events.jsonl`) → listed but flagged "(empty)"; restart still allowed (resumes an empty conversation).
- **Indexing in progress** → list renders from metadata immediately; a subtle "indexing content…" indicator until the content index is ready; search degrades to metadata-only until then.

## 6. Data model

No change to Copilot's files (read-only). New in-memory/types:

```go
// package copilot (cross-platform)
type Session struct {
    ID         string    // workspace.yaml id (== folder name)
    Dir        string    // absolute path to the session folder
    Name       string    // workspace.yaml name (may be empty)
    Repository string    // e.g. "thirschel/Hangar"
    OriginRoot string    // FROZEN origin repo path — from earliest session.start.context.gitRoot
    OriginHead string    // FROZEN base commit — from session.start.context.headCommit
    OriginRef  string    // FROZEN original branch name — from session.start.context.branch
    Cwd        string    // workspace.yaml cwd (mutable; informational only)
    CreatedAt  time.Time
    UpdatedAt  time.Time
    HasEvents  bool      // events.jsonl present & non-empty
    InUse      bool      // a *fresh* inuse.*.lock is present (PID alive / recent mtime)
}

func (s Session) DisplayName() string // name → first-user-msg snippet → "repo@branch" → short id
```

> **§4-note — origin freeze (why `OriginRoot` ≠ `workspace.yaml.git_root`):** resuming a session launches Copilot in the *new* worktree, and Copilot is expected to rewrite `workspace.yaml`'s `cwd`/`git_root` to that worktree path. If we re-read `git_root` from YAML on every browse, a session's "origin" would silently drift to a throwaway worktree after the first restart (and a second restart would target a deleted directory). We therefore snapshot the origin from the **immutable** `session.start` event the first time we index a session and persist it; YAML `git_root` is used only when no `session.start` exists.

Search index (cached on disk under `~/.hangar/copilot-index.json`, guarded by a lockfile; cleared by `cs reset`):

```go
const indexSchemaVersion = 1  // bump to force a full rebuild on format/cap changes

type indexEntry struct {
    ID         string
    Size       int64     // events.jsonl size  ┐ invalidation: re-index when
    ModTime    time.Time // events.jsonl mtime ┘ size OR mtime changes
    SchemaVer  int       // == indexSchemaVersion, else entry is stale
    OriginRoot string    // frozen origin snapshot (see §4-note)
    OriginHead string
    OriginRef  string
    Haystack   string    // lowercased: name + repo + branch + message text (capped, see §7.1)
}
```

Persistence (`session/storage.go` `InstanceData`): add `AgentSessionID string` so a restarted instance re-launches with `--resume=<id>` after a TUI restart, including through the **paused** reconstruction path (§7.5).

## 7. Architecture & implementation

### 7.1 New package `session/copilot/` (cross-platform — no build tags)
- `Root() string` — resolve session-state root: `%USERPROFILE%\.copilot\session-state` on Windows, `$HOME/.copilot/session-state` elsewhere (overridable via env, e.g. `CS_COPILOT_SESSION_DIR`, for tests).
- `Discover() ([]Session, error)` — list child dirs, parse each `workspace.yaml` (use `gopkg.in/yaml.v3` — already an indirect dep, see §11; or hand-parse the flat file), detect `events.jsonl`/fresh `inuse.*.lock`, and read the **frozen origin** (`OriginRoot/Head/Ref`) from the first `session.start` event (one short streamed read; falls back to YAML `git_root` only when absent). Fast: small reads only. Resilient: a bad YAML file yields a minimal `Session{ID: dirname}` rather than failing the whole scan. Parallelized across `runtime.NumCPU()` workers; per-session errors are logged via `hangar/log` and counted (surfaced as a footer "N skipped"), never fatal.
- `FirstUserMessage(s Session) (string, error)` — stream `events.jsonl`, return the first `user.message` `data.content`; stops at the first hit; never loads the whole file. Used for the display-name fallback and the preview snippet (so list navigation never blocks on a synchronous full read).
- **Index** — `BuildIndex(ctx, sessions)` builds/refreshes `~/.hangar/copilot-index.json` under a lockfile; `Search(ctx, query) []Session` ranks results.
  - **Cap:** index up to **32 KB** of concatenated `user.message`/`assistant.message` text per session (configurable). This bounds memory (~445 × 32 KB ≈ 14 MB) and time. **Indexing uses `data.content`** (the human-authored text); `transformedContent` is excluded to avoid indexing injected boilerplate.
  - **Capped-search caveat (resolves an AC conflict):** because content is capped, a term that appears *only* beyond 32 KB of a very long transcript can be missed. AC §8 is written against the *indexed* haystack. A `Ctrl+f` "deep search" affordance MAY stream the full `events.jsonl` for the *currently filtered* subset as a fallback (bounded, cancellable) — optional, Phase 3.
  - **Invalidation:** re-index a session when its `events.jsonl` **size or mtime** changed, or when `SchemaVer != indexSchemaVersion`. (mtime alone is unreliable; size+mtime+schema-version is cheap and robust.)
  - **Concurrency:** all disk work runs off the UI goroutine via `tea.Cmd`; `Search` takes a `context.Context` so each new keystroke cancels the previous (debounced) search. A single in-process build runs at a time; `Ctrl+r` coalesces rather than launching a second build. The on-disk index is lock-guarded so concurrent `cs` processes don't corrupt it.

### 7.2 New UI component `ui/sessionBrowser.go`
- `SessionBrowser` struct: `sessions`, `filtered []copilot.Session`, a `textinput.Model` (Bubbles) for search, `selectedIdx`, `width/height`, `focus` (search|preview), and a handle to the index.
- `SetSize`, `HandleKeyPress(msg) (action, …)`, `SetQuery(string)`, `GetSelected() *copilot.Session`, `String()` (renders search bar + list + preview, mirroring `ui/list.go` styling).
- Search recompute is triggered by a debounced `tea.Cmd` (mirror the existing debounced branch-search pattern used by the prompt overlay's branch picker).

### 7.3 `app/app.go` wiring
- Add `stateBrowse` to the `state` enum, plus `*ui.SessionBrowser` and `resumedSessionIDs map[string]bool` fields on `home`.
- `handleKeyPress`: in `stateDefault`, `KeyBrowse` (`b`) → load sessions (async `tea.Cmd` returning a `sessionsLoadedMsg`) and set `state = stateBrowse`.
- **Key-routing precedence (important):** today `q` triggers a **global quit** and `ctrl+c` quits *before* most state-specific routing, and the menu-underline logic only exempts `statePrompt/Help/Confirm`. The browser has a focused **text input**, so `stateBrowse` must be handled **before** the global `q`/quit and menu-highlight branches, and ordinary runes (including `q`) must be routed to the search box — only `Esc` (and `ctrl+c`) exit the browser. Mirror how `statePrompt` already short-circuits global keys.
- In `stateBrowse`: route keys to the browser; on debounced query change, dispatch the (cancellable) search cmd; on `Enter`, run the **restart flow**; on `Esc` (or `ctrl+c`), return to `stateDefault`.
- `View()`: when `state == stateBrowse`, render `m.sessionBrowser.String()` full-screen instead of the list/preview split.
- `updateHandleWindowSizeEvent`: size the browser like the main content area.

### 7.4 Restart flow (the core action)
On `Enter` over a selected `copilot.Session s`:

1. **Pre-flight guards (hard, not warnings):**
   - **Already resumed:** if `s.ID` is in the `home` model's in-process `resumedSessionIDs` set (a session already resumed in this TUI), refuse with a message ("already open as workspace <title>") and select that workspace instead. Prevents two Hangar workspaces writing the same session folder.
   - **In-use elsewhere:** re-check for a **fresh** `inuse.*.lock` *immediately* before launch (not just at browse time). If present, **block** and explain (resuming a live session risks corrupting `events.jsonl`); offer to open read-only preview only.
   - **Origin repo missing:** resolve target repo = `s.OriginRoot`. If it's empty, `os.Stat`-fails, or isn't a git repo, fall back to the **current** repo `cs` was launched in (with a clear warning that file context won't match), rather than erroring out. (Never fall back to `s.Cwd`, which is usually a deleted worktree path.)
2. If the resolved repo ≠ current repo, show a `stateConfirm` confirmation ("Resume in <repo>? A new worktree/branch will be created there.").
3. **Build the instance with explicit, safe identity + branch:**
   ```go
   suffix := s.ID[:6]                                  // disambiguate duplicates
   title  := s.DisplayName() + " (resume " + suffix + ")"
   inst, _ := session.NewInstance(session.InstanceOptions{
       Title:   title,                 // unique → unique host session name (§7.4-id)
       Path:    targetRepo,            // resolved origin repo (frozen), not s.Cwd
       Program: copilotProgram,        // resolved "copilot …" launch command
       // Branch is NOT set → a NEW, uniquely-named branch is created (see below),
       // we never check out / overwrite the session's ORIGINAL branch.
   })
   inst.AgentSessionID = s.ID          // marks this instance as a RESUME (→ --resume=<id>)
   inst.BaseCommit     = s.OriginHead  // new branch is based on the session's recorded HEAD
   ```
4. `inst.Start(true)` creates a **new** worktree+branch in `targetRepo`:
   - The branch name is `BranchPrefix + sanitize(title)` — unique because `title` carries the id suffix, so resuming the same session twice cannot collide (the TUI worktree path does **not** add its own uniqueness suffix today — see §7.4-id).
   - The branch is created from `s.OriginHead` when available (so the resumed conversation sees the files it was working from), else from the repo's current `HEAD` (with a note).
   - **Never** reuse/check-out/delete the session's original branch (`s.OriginRef`). That branch may hold unpushed work and/or be checked out elsewhere; touching it risks data loss.
   - The launch command includes `--resume=<s.ID>` (see §7.5). **No initial prompt is sent** — Copilot restores history itself; auto-title generation is not triggered (Title is fixed here).
5. Add `s.ID` to `resumedSessionIDs`, add the instance to the list, persist, return to `stateDefault` with the new workspace selected.

> **§7.4-id — identity uniqueness (P0):** the Windows host rejects a duplicate **session name** (`host_windows.go startManagedSession`) and the sanitized **Title** is what becomes that name (`session_windows.go`). The TUI worktree branch path (`session/git/worktree.go NewGitWorktree`) does **not** add a uniqueness suffix (only the daemon path appends `-id[:6]`). Both collide on a duplicate Title. The `(resume <id6>)` suffix fixes both at once; add tests for "resume the same session twice".

### 7.5 Injecting the resume flag into the Instance launch path
The TUI Instance path launches `i.Program` **raw** today. Introduce a shared, cross-platform helper and use it everywhere the program is launched.

- **Two intents, two helpers** (do NOT collapse into one) in new `session/agentcmd.go`:
  ```go
  func SupportsResume(program string) bool       // argv-aware: executable basename == "copilot"
  func SeedNewCommand(program, id string) string // copilot → program + " --session-id=" + id
  func ResumeCommand(program, id string) string  // copilot → program + " --resume="     + id
  ```
  A single always-`--resume` helper would **break new-session creation**, because `--resume=<id>` requires an *existing* id whereas a brand-new session must *seed* its id with `--session-id`. `SupportsResume` matches the **executable basename** of the parsed command (not a naïve `strings.Contains("copilot")`, which would false-match e.g. a path or prompt text).
- Add `AgentSessionID string` and `BaseCommit string` to `Instance`, and a method:
  ```go
  func (i *Instance) launchCommand() string {
      if i.AgentSessionID != "" && SupportsResume(i.Program) {
          return ResumeCommand(i.Program, i.AgentSessionID) // browser-created resume
      }
      return i.Program
  }
  ```
- **Apply `launchCommand()` at ALL THREE launch sites** (this is the most-missed point):
  1. `Instance.Start()` — first-time start.
  2. `FromInstanceData()` **paused branch** — paused instances reconstruct `termSession = NewTerminalSession(Title, Program)` directly **without** calling `Start()`; if this site isn't fixed, a resumed→paused→TUI-restart→unpaused session silently loses `--resume` and starts a fresh conversation.
  3. `Resume()` — un-pausing restarts the terminal session.
- **Persist/restore** `AgentSessionID` (and `BaseCommit`) in `session/storage.go` `InstanceData` so resume survives a TUI restart.
- **Refactor, don't duplicate:** move the existing winhost `supportsResume`/`agentLaunchCommand` logic into `session/agentcmd.go` and have `winhost` call it. Keep the daemon `create()` path on **`SeedNewCommand`** (new id) and switch `reviveBySession()` to **`ResumeCommand`** (existing id). Add a regression test asserting the daemon's emitted command for existing callers is unchanged where intended.

> **Launch-path decision (resolved):** extend the **TUI `Instance` path** (above). All three reviewers agreed: it keeps the browser consistent with the `n`/`N` flow, isolates TUI sessions in `state.json` from daemon workspaces, and needs **no** change to the winhost RPC protocol (the resume flag is baked into the program string before `NewTerminalSession`). Routing through the daemon `workspaceManager` would additionally require new proto/client fields (`CreateWorkspace` has no caller-supplied agent-session-id field today).

### 7.6 Affected files
- `session/copilot/` (new) — discovery, origin-freeze, first-message, on-disk index, cancellable search (+ tests).
- `ui/sessionBrowser.go` (new) — browser component: search input, results, async preview (+ tests).
- `app/app.go` — `stateBrowse`, key-routing precedence, `resumedSessionIDs`, async load/search cmds, restart flow + pre-flight guards, render, sizing.
- `keys/keys.go` — `KeyBrowse` (+ binding `b`) and in-browser keys.
- `ui/menu.go` — menu entry + browser `MenuState`.
- `session/instance.go` — `AgentSessionID` + `BaseCommit` fields, `launchCommand()`, applied at `Start()`, the paused branch of `FromInstanceData()`, and `Resume()`; base-commit handling in worktree setup.
- `session/storage.go` — serialize/restore `AgentSessionID` and `BaseCommit` (absent → empty, back-compatible).
- `session/agentcmd.go` (new, cross-platform) — `SupportsResume` (argv-aware) + `SeedNewCommand`/`ResumeCommand`; `winhost/workspace_windows.go` refactored to reuse it (`create()`→seed, `reviveBySession()`→resume).
- `ui/list.go` — disambiguate `RepoName`/`repos` keying for cross-repo sessions (key by repo path/owner-repo, not basename) so the browser's multi-repo workspaces group/count correctly.
- `log/` — log + count skipped/failed sessions during discovery/indexing.
- `app/help.go`, `README.md` — document the screen + keys ("b — browse Copilot sessions").

## 8. Acceptance criteria

1. Pressing `b` from the default view opens a full-screen Session Browser that lists **all** local Copilot sessions (across every repo), most-recently-updated first, with a visible total count. While the browser is open, ordinary keys (including `q`) edit the search box and do **not** quit the app; only `Esc`/`ctrl+c` exit the browser.
2. Each row shows a non-empty **display name** even when `workspace.yaml` has no `name` (fallback to first user message, then `repo@branch`, then short id), plus repo, branch, and relative updated time.
3. Typing a query filters the list **live** (debounced) and matches against **both** metadata **and** indexed conversation text; multi-term queries use AND semantics; matching is case-insensitive; the count updates.
4. Search remains responsive (no perceptible input lag, target <50 ms p99 on a warm index) with at least **445** sessions present; discovery, indexing, and search run off the UI thread and a new keystroke cancels the previous search.
5. Selecting a session and pressing `Enter` creates a **new Hangar workspace** whose agent is launched with `copilot --resume=<that session's id>` (full UUID), and the resumed agent shows the **prior conversation history**. A brand-new Copilot session created via `n`/`N` is launched with `--session-id=<new-uuid>` (seed) — the two intents never use the same flag.
6. The new workspace's git worktree is created in the session's **frozen origin repo** (`OriginRoot`, snapshotted from `session.start`), on a **new uniquely-named branch** based on the session's recorded `OriginHead`; when the origin repo differs from the current repo, the user confirms first; when the origin repo is missing on disk, it falls back to the current repo with a warning (never to a deleted `cwd`).
7. The restart **never reuses, checks out, or deletes the session's original branch**; resuming the **same** session **twice** produces two distinct workspaces (distinct titles/branches) without a host-session-name or `git worktree add` collision.
8. The created workspace appears in the normal list, is selected, and **continues to resume the same Copilot session after a TUI restart** — including when it was **paused** before the restart (the paused-reconstruction path applies the resume flag).
9. The origin repo used for a session is **stable across repeated restarts** — a second restart of the same session still targets the original repo, even though Copilot may have rewritten `workspace.yaml`'s `cwd`/`git_root` after the first restart.
10. The browser is **read-only** toward Copilot's files (verified against a read-only fixture dir) and never deletes/edits anything under `~/.copilot/session-state`.
11. Resuming a session that is currently **in use** (fresh `inuse.*.lock`) or already resumed in this TUI is **blocked** (not silently allowed), with a clear message.
12. Edge inputs never crash: missing session-state dir, empty session (no `events.jsonl`), malformed `workspace.yaml`. Skipped/failed sessions are logged and surfaced as a footer count.
13. `cs reset` removes the on-disk index; a stale index (schema-version or size/mtime mismatch) is transparently rebuilt.
14. The `session/copilot` and `session/agentcmd` packages build and their tests pass on **Windows and Unix**; `go build ./...` and `go test ./...` pass on both.

## 9. Test plan

Follow the repo's table/unit style (`testify/require`; see `ui/list_test.go`, `session/instance_test.go`, `session/winhost/workspace_windows_test.go`). Discovery/search/command-building must be **pure and filesystem-fixture-driven** (point `copilot.Root()` at a temp dir via env override) so they need no real Copilot install.

**Discovery / origin-freeze (`session/copilot`):**
- Parses a fixture session-state dir: correct `ID`, `Name`, `Repository`, timestamps.
- `OriginRoot/Head/Ref` are taken from `session.start` even when `workspace.yaml` `git_root` differs (simulating Copilot's post-resume rewrite) — verifies origin is frozen/stable.
- `git_root` empty **and** `session.start` absent → documented fallback; deleted `cwd` is **not** used.
- Missing `name` → `DisplayName()` falls back through each tier; missing/empty `events.jsonl` → `HasEvents=false`, still discoverable; malformed `workspace.yaml` → minimal `Session{ID}`, other sessions unaffected.
- Fresh vs. stale `inuse.*.lock` → `InUse` true only when fresh. Cross-platform `Root()` honors the env override.

**Search / index:**
- Metadata-only match; content match (term only in a message); multi-term AND; case-insensitivity; no-match → empty.
- Ranking: most-recent default; relevance when a query is present (deterministic tiebreaks).
- **Invalidation by size+mtime+schema-version**: changing fixture `events.jsonl` size/mtime re-indexes only that entry; bumping `indexSchemaVersion` forces a full rebuild.
- Cap behavior: an oversized `events.jsonl` is truncated to the cap without error; a term beyond the cap is (documented as) not matched by the indexed search.
- `Search` honors context cancellation.

**Command builders (`session/agentcmd`):**
- `ResumeCommand("copilot", id)` → `copilot --resume=<id>`; `SeedNewCommand("copilot", id)` → `copilot --session-id=<id>`; with extra args (`copilot --banner`) the flag is appended once.
- `SupportsResume` is argv-aware: matches `copilot`/`copilot.cmd` basename, **rejects** `claude`, `aider …`, and strings that merely contain "copilot" in a path/prompt.
- Empty id → unchanged.
- **Daemon no-op regression:** the command emitted for `create()` (seed) and `reviveBySession()` (resume) is exactly as intended after the refactor (extends existing `workspace_windows_test.go`).

**Instance + storage (`session`):**
- `launchCommand()` reflects `AgentSessionID`; applied at `Start()`, the **paused branch** of `FromInstanceData()`, and `Resume()` (assert via injected/fake terminal).
- `AgentSessionID`/`BaseCommit` round-trip through `InstanceData`; absent fields load as empty (back-compat with old `state.json`).
- **Branch safety:** restart builds the worktree from `BaseCommit` and creates a **new** branch; the session's original branch ref is untouched (assert it still points at its original commit). **Resume-twice:** two restarts of the same session yield distinct branch names and succeed.

**UI (`ui/sessionBrowser_test.go`):**
- Filtering updates `filtered`; selection clamps when results shrink; `↑/↓` wrap; selection preserved across query changes where the item remains; empty-state and "no matches" rendering.
- Preview snippet is produced without a synchronous full-file read (uses the in-memory haystack / async cmd).

**App (`app/app_test.go`):**
- `b` transitions `stateDefault → stateBrowse`; `q` types into search (does not quit); `Esc` returns; existing flows unaffected.
- Restart of a fixture session builds an `Instance` with `Path == OriginRoot`, `AgentSessionID == session.id`, unique title; cross-repo restart routes through confirmation; in-use / already-resumed restart is blocked.

**Manual / integration (gated, requires a real Copilot install):**
- Resume a real session and confirm prior conversation history is restored in the attached console (unit tests can only assert the command string).

## 10. Edge cases

- **cwd-rewrite cascade** — after the first restart, Copilot rewrites `workspace.yaml`'s `cwd`/`git_root` to the throwaway worktree. The frozen `OriginRoot` (from `session.start`) ensures a second restart still targets the original repo, not a deleted worktree dir.
- **Resume the same session twice** — distinct titles/branches via the `(resume <id6>)` suffix; the in-process `resumedSessionIDs` guard prefers selecting the existing workspace.
- **In-use session** — a **fresh** `inuse.*.lock` (PID alive / recent mtime) **hard-blocks** resume (two writers would corrupt `events.jsonl`); a stale lock is ignored.
- **Origin branch holds unpushed work** — never touched; restart always makes a new branch, so no data loss.
- **Origin repo missing/moved** — `OriginRoot` not on disk → fall back to current repo with a warning; never error out, never use the (likely deleted) `cwd`.
- **Original `OriginHead` no longer exists** (history rewritten / shallow clone) → fall back to current `HEAD` for the new branch, with a note.
- **Huge `events.jsonl` (~350 KB+)** — content read is capped and streamed; never blocks the UI; term beyond cap documented as not indexed.
- **Hundreds–thousands of sessions** — first paint is metadata-only; content index builds in the background (NumCPU workers) and is cached between runs; progress shown.
- **Duplicate display names** — disambiguated by repo/branch/short-id in the row and by the id suffix in the created workspace.
- **Cross-repo `RepoName` collision** — two origin repos with the same basename must not collapse into one bucket in `ui/list.go` (`repos` keyed by full repo path/owner-repo).
- **Non-UTF-8 / partial last line in `events.jsonl`** — tolerate; skip unparsable lines; count + log.
- **Concurrent index access** — multiple `cs` processes / `Ctrl+r` during a build: lockfile + coalescing; no torn cache.
- **Timezone/format** — timestamps are UTC RFC-3339; rendered as local relative time.

## 11. Risks & open questions

**Resolved by the multi-model review (now baked into the doc):**
- **Launch path** — decided: extend the TUI `Instance` path (§7.5).
- **Flag semantics** — decided: two helpers — `--session-id` to seed new, `--resume` to resume (§7.5). *Accepted alternative:* unify on `--session-id=<id>` for both (it resumes existing and seeds new), which slightly reduces helper count at the cost of fail-loud behavior on a stale id. Pick one before coding; the doc assumes the two-helper form.
- **Continue-in-place** — confirmed desired: resume writes back into the original `<uuid>` folder (user-confirmed "continue the same conversation"); concurrency hazard mitigated by the in-use hard-block + `resumedSessionIDs` guard (§7.4).
- **YAML dependency** — resolved: `gopkg.in/yaml.v3` is already an **indirect** dep (`go.mod`); promote to direct via `go mod tidy`, or hand-parse the flat file (no nesting).
- **Origin drift** — resolved: freeze `OriginRoot/Head/Ref` from `session.start` (§4-note).

**Residual open questions:**
1. **Capped-search recall** — 32 KB/session cap can miss terms deep in very long transcripts. Acceptable for v1? Add the optional `Ctrl+f` full-stream "deep search" in Phase 3, or raise/lift the cap (memory cost)?
2. **`events.jsonl` format stability** — undocumented; may change across Copilot versions. Parser must degrade to metadata-only on unexpected shapes. Pin a tested Copilot version range in docs.
3. **Base-commit policy** — base the new branch on the session's `OriginHead` (matches original files but may be old) vs. current `HEAD` (fresh but mismatched). Doc proposes `OriginHead` with `HEAD` fallback; confirm.
4. **Index privacy** — conversation text is cached under `~/.hangar/`. Local-only, but document it; consider an opt-out / no-content-index mode.
5. **Keybinding** — confirm `b` (vs. `S`) and the in-browser map (e.g. `Ctrl+r` reindex, `Ctrl+f` deep search) don't surprise users.
6. **Preview fidelity** — render raw text, or strip ANSI / render markdown? Pick for §5.1.

## 12. Phased delivery / milestones

1. **Discovery + metadata search** — `session/copilot` discovery with **origin-freeze** (`OriginRoot/Head/Ref` from `session.start`), display-name fallback, metadata-only search; `ui/sessionBrowser.go`; `stateBrowse` + `b` key with correct key-routing precedence; async preview; menu/help. (+ tests)
2. **Resume (current repo)** — `session/agentcmd.go` (`SupportsResume`/`SeedNewCommand`/`ResumeCommand`); `Instance.AgentSessionID`+`BaseCommit`, `launchCommand()` applied at **all three** launch sites, storage round-trip; restart flow with **safe branch creation** (new uniquely-named branch from `OriginHead`, original branch untouched), **identity-uniqueness** suffix, and **in-use / already-resumed guards**; winhost refactor + daemon no-op regression test. (+ tests)
3. **Content search + indexing** — lock-guarded on-disk index (`copilot-index.json`), size+mtime+schema-version invalidation, NumCPU workers, cancellable debounced search, ranking, 32 KB cap (+ optional `Ctrl+f` deep search); `cs reset` clears the index. (+ tests)
4. **Multi-repo restart** — worktree in `OriginRoot`, cross-repo confirmation, origin-missing fallback; `ui/list.go` repo-key disambiguation for cross-repo grouping/counts. (+ tests)
5. **Polish & docs** — in-use/empty/error states + skipped-session footer count, README/docs, regression pass, gated real-`copilot --resume` integration check; sketch the macOS/Linux wiring for the (already cross-platform) discovery layer.
