# Hangar Desktop — Handoff

A pick-up-and-go guide for **Hangar Desktop**, the **Windows desktop app** (a Conductor-like GUI for running parallel
coding agents in isolated git worktrees) and its **Go core-daemon**. Read this first; it complements:

- **`docs/native-windows.md`** — the lower-level native-Windows **session-host / daemon** model (ConPTY,
  attach handshake, AutoYes, persistence, protocol version skew). Still accurate for the transport layer.
- **`desktop/PLAN.md`** — the **E0–E6 phase roadmap**, UI design, and Conductor feature mapping.
- **`desktop/PACKAGING.md`** — building the installer, auto-update, and code-signing.

> **Branch:** all of this lives on **`desktop-core-daemon`** (a fork-only branch; upstream
> `smtg-ai/claude-squad` is intentionally untouched). It has **not** been PR'd/merged to the fork's
> default branch yet.

---

## 1. What this is

A desktop app that runs **multiple AI coding agents in parallel**, each in its own **git worktree +
branch**, with a live terminal, a diff/review panel, and a run/preview panel — modeled on
[conductor.build](https://www.conductor.build).

**Architecture = Electron front-end + Go "core daemon" (the Wave Terminal model).** The Go
session-host was promoted into a daemon that owns the whole workspace lifecycle (worktrees, ConPTY
agent sessions, diff, persistence). The Electron app is a **thin client** that renders the agent's
terminal stream in xterm.js and drives everything over RPC. **No orchestration is reimplemented in
Node.** Windows-first; the Unix/tmux TUI path is unchanged.

```
  Electron app (desktop/, Windows)                     core daemon = cs.exe (Go session-host)
  ┌───────────────────────────────┐                    ┌──────────────────────────────────────────┐
  │ renderer (React + xterm.js)   │  preload  ipcMain  │ control pipe  \\.\pipe\hangar-host-<SID>
  │  Sidebar / CenterPane(tabs)   │◄────────►│ host-   │   length-prefixed JSON RPC (proto v3)      │
  │  Review / Run panels          │          │ client  │ ── workspaces: worktree+branch+session     │
  └───────────────────────────────┘          │ (TS)    │ ── ConPTY sessions + x/vt emulator         │
            ▲  per-session attach streams ─────────────│ ── git diff/commit/push, run processes     │
            └───────── \\.\pipe\hangar-att-… ──────│ ── AutoYes, persistence, status sampling   │
                                                        └──────────────────────────────────────────┘
```

- **Control pipe** (one per user, SID-named): request/response RPC. The app discovers/launches it via
  `ensureHost`.
- **Attach streams** (one pipe per attached session): after an `Attach` RPC the daemon returns a
  per-session pipe + one-time token; the app streams raw VT bytes both ways. The app keeps a
  `Map<sessionName, socket>` so the **agent and an in-worktree shell stream concurrently**.
- **Protocol is v5** (`session/winhost/proto/proto.go`, `Version = 5`). The app sends its version in
  `Hello`; it requires daemon **≥ v5**. Adding fields to `WorkspaceInfo` is additive and does **not**
  bump the version; new **methods** do. (v4 = agent-generated titles; v5 = Regenerate/ForceRegenerate.)
  The renderer reads `PROTO_VERSION` from the node-free `desktop/src/shared/proto-version.ts` (mirrored
  in `main/host-client.ts`), so the value lives in one place across main + renderer.

---

## 2. Feature inventory (what's built, with the commit that introduced it)

**Daemon (Go, `session/winhost/`):**
- `eabc5bb` **E1** — core-daemon workspace RPC: `ListWorkspaces`/`CreateWorkspace`/`GetWorkspace`/
  `ArchiveWorkspace`/`WorkspaceDiff` + `workspaceManager` owning worktree+session, persisted to
  `workspaces.json`.
- `de6c8dc` **E3a** — `WorkspaceCommit` / `WorkspacePush` git review RPCs.
- `3efc273` **E4** — run-process manager (**proto v3**): `StartRun`/`StopRun`/`WorkspaceRunOutput`,
  ring-buffered output, localhost preview-URL detection, taskkill-tree on stop.
- `f334961` — fail-fast agent validation (`exec.LookPath` before creating anything) + **panic-recovery**
  on the dispatch + session goroutines (recovered panics → `host.log`).
- `9968b12` — run all daemon-spawned console children **windowless** (`hideConsole` / `CREATE_NO_WINDOW`).
- `2d7ac96` — stable list order + **revive-on-attach** (a dead workspace session is resurrected from
  persisted metadata on the next attach).
- `2961cdb` / `975aad1` — per-workspace **status** sampling (`busy`/`waiting`), ignoring user input-echo.
- `2322039` — broaden **`detectWaiting`** (status-only) beyond approval prompts.
- `b56761d` — **resume the agent conversation** across daemon restarts via copilot `--session-id=<uuid>`.
- *(v5, pending commit)* **Regenerate Agent** — `RegenerateAgent`/`ForceRegenerate` RPCs: kill the current
  agent and start a fresh one (rotated `SessionName` + `AgentSessionID`) in the same worktree, optionally
  first asking the live agent to write `HANDOFF.md` and seeding the new agent with it. Inactivity/hard-cap
  completion via the pure `handoffReady`, deterministic transcript fallback, archive-safe (tombstone +
  wait-on-done), revivable-on-attach. See `desktop/features/regenerate-agent.md`.

**Desktop app (`desktop/src/`):**
- `4c085a4` **M0** — migrated the Electron app into the monorepo as `desktop/`.
- `b145507` **E2/E3/E4 UI** — sidebar (create/list/archive), live agent terminal + composer + AutoYes,
  Review panel (changed files + rich diff + commit/push), Run panel (command + output + preview).
- `51389f7` **E5** — system tray, global hotkey (`Ctrl+Shift+Space`), Settings modal, in-app shortcuts,
  native notifications, app/tray icon, file logger.
- `bc416a8` **E6** — electron-builder NSIS installer bundling the daemon, electron-updater, packaging docs.
- `2961cdb` — sidebar **status indicators** (spinner/pulse/steady/dim).
- `2322039` — **auto-reconnect** the control pipe when the daemon restarts.
- `152dcc7` — **Files tab** (read-only worktree browser) + **Terminal tab** (PowerShell in the worktree,
  concurrent with the agent) + the session-scoped stream refactor (`TermView`).
- *(v5, pending commit)* **Regenerate Agent UI** — `↻ Regenerate` button beside AutoYes, `RegenerateModal`
  (handoff checkbox + live phase progress + **Kill now**), composer disabled while regenerating, and
  broadened "Agent finished" suppression so a regenerate never fires a false finished toast.
- *(pending commit)* **Multi-agent grid** — mark 2+ agents in the sidebar (`Sidebar` checkboxes /
  `gridSelectedIds`) and open an in-window grid (`▦ Grid` / `g`) of live, focusable agent terminals
  (`GridPane` tiles, one `TermView` per session); click a tile and type straight into that agent, and
  **drag a tile by its header handle to rearrange** (tiles are keyed by id so the live terminals
  survive the move; order persists in `localStorage`). Rows are ≥500px and per-row resizable (drag a
  tile's bottom edge; heights persist). Renderer-only: reuses the existing per-session
  stream / `sendInput` / `resize` IPC (no daemon or proto change). Agents-per-row control defaults to
  Auto (by width), persisted in `localStorage`.

---

## 3. Where things live

**Daemon (`session/winhost/`):**
| File | Responsibility |
|---|---|
| `proto/proto.go` | Wire protocol (v3): methods, `Request`/`Response`, `WorkspaceInfo`, framing. Platform-neutral. |
| `host_windows.go` | `host`: `dispatch()` routing, `safeDispatch` (panic recovery), `attachSession` (+ revive), `startManagedSession`, idle loop, `RunHost`. |
| `workspace_windows.go` | `workspaceManager`: create/list/get/archive/diff/commit/push, run handlers, `reviveBySession`, persistence, `agentLaunchCommand`/`supportsResume`/`newUUID`. |
| `conpty_windows.go` | `conptySession`: ConPTY + `x/vt` emulator, drain/wait/autoYes loops, `updateStatus`/`agentStatus`, `detectPrompt` (AutoYes) + `detectWaiting` (status). |
| `run_windows.go` | `runManager`: per-workspace run process, ring buffer, preview-URL regex, taskkill-tree. |
| `attach_windows.go` | Client-side console hand-off for the TUI's `cs attach` (Ctrl-Q detaches). |
| `client_windows.go` | Go `Client` (used by tests + the TUI). |
| `noconsole_windows.go` | `hideConsole(cmd)` — `CREATE_NO_WINDOW`. **Use on every console child.** |
| `lock_windows.go` / `paths.go` | Singleton host lock; `~/.hangar` path helpers; host.json discovery. |

**Desktop main process (`desktop/src/main/`):**
| File | Responsibility |
|---|---|
| `index.ts` | App lifecycle, window, tray/hotkey, **all `ipcMain` handlers**, the per-session attach `Map`, shell lifecycle, fs IPC. |
| `host-client.ts` | `ControlClient` (framed JSON over the pipe), `ensureHost`, attach-stream connect, all TS types mirrored from `proto.go`. |
| `settings.ts` | Read/write `config.json` (daemon) + `desktop.json` (app-only) settings. |
| `tray.ts` / `updater.ts` / `logger.ts` / `assets.ts` | Tray; electron-updater; file logger (`desktop.log`); packaged-asset path resolver. |

**Desktop renderer (`desktop/src/renderer/src/`):**
| File | Responsibility |
|---|---|
| `App.tsx` | Top-level state, poll loop, connection banner, keyboard shortcuts, notifications wiring. |
| `components/Sidebar.tsx` | Workspace list + **status indicator**, create form, archive. |
| `components/CenterPane.tsx` | Tabbed surface: Agent / Files / Terminal (all mounted, CSS-toggled). |
| `components/TermView.tsx` | **Reusable** session-scoped xterm (copy/paste, ConPTY) — used by the agent and the shell. |
| `components/GridPane.tsx` | Multi-agent grid: tiles a `TermView` per selected agent; per-row control, focus ring, drag-to-reorder. |
| `components/grid-columns.ts` | Pure "agents per row" math (Auto/effective/cycle); mirrors the Go TUI helper. |
| `components/grid-reorder.ts` | Pure drag-and-drop reorder helper (move a tile id to a target's slot). |
| `components/grid-rows.ts` | Pure per-row height helpers (clamp / normalize / set; 500px floor). |
| `components/ShellTerminal.tsx` | Lazily ensures `sh_<wsId>` PowerShell, renders a `TermView`. |
| `components/FilesPanel.tsx` | Lazy file tree + read-only viewer. |
| `components/ReviewPanel.tsx` | Changed files + rich diff + commit/push. |
| `components/RunPanel.tsx` | Run command + live output + preview-URL "Open". |
| `components/Composer.tsx` / `SettingsModal.tsx` | Agent message box; settings UI. |

`desktop/src/preload/index.ts` is the `window.cs` bridge (typed IPC). `desktop/go.mod` is a deliberate
module boundary (see gotchas).

---

## 4. Build / run / test

> **The system `go` is NOT on PATH.** Use the portable toolchain and these env vars.

**Go (daemon) — Windows:**
```pwsh
$go = "$env:TEMP\goroot125\go\bin\go.exe"
$env:GOTOOLCHAIN = "local"; $env:GOFLAGS = "-mod=mod"
& $go build ./...
& $go test ./...
# gofmt: the *.go glob fails in PowerShell — pass EXPLICIT file paths:
& "$env:TEMP\goroot125\go\bin\gofmt.exe" -w session\winhost\conpty_windows.go session\winhost\host_windows.go
```

**Go — Linux verify (must ALSO pass; use the hardcoded path, do NOT munge PATH):**
```pwsh
wsl -d Ubuntu bash -lc 'cd /mnt/d/dev/Hangar; export GOTOOLCHAIN=local GOFLAGS=-mod=mod; /home/bendog/go125/go/bin/go build ./... && /home/bendog/go125/go/bin/go test ./...'
```
> **Rule:** keep `go build/test ./...` green on **Windows AND Linux** for every change. Windows-only code
> goes in `*_windows.go`; provide a `!windows` stub when a symbol is referenced cross-platform
> (e.g. `noconsole_other.go`, `runhost_other.go`).

**Rebuild the daemon binary the app launches** (after any Go change):
```pwsh
& $go build -o dist\cs.exe .    # the app spawns D:\dev\Hangar\dist\cs.exe (packaged: resources\dist\cs.exe)
& $go build -o cs.exe .         # the root binary too, to avoid version skew
```

**Desktop app:**
```pwsh
cd desktop
npm install
npm run dev          # electron-vite dev (spawns the daemon via ensureHost)
npm run lint ; npm run typecheck ; npm run build
npm run dist         # NSIS installer -> desktop/release/ (see PACKAGING.md)
```

**Testing a worktree (the singleton-daemon trap).** The daemon is a per-user singleton: the app
connects to whichever `cs.exe` already owns the pipe, so a stale daemon/app from another checkout gets
reused and you test the *wrong* code (e.g. `daemon is v4 — the app needs v5`). Use the helper to make
the current worktree authoritative — it rebuilds this worktree's `dist\cs.exe`, stops the running
app + daemon (freeing the pipe), and launches `npm run dev` with `CS_EXE` pinned:
```pwsh
.\desktop\scripts\dev-worktree.ps1            # rebuild + relaunch from this worktree
.\desktop\scripts\dev-worktree.ps1 -SkipBuild # just re-pin app/daemon, no Go rebuild
.\desktop\scripts\dev-worktree.ps1 -NoApp     # rebuild daemon + free pipe only
```
Run it from an **external** terminal, not an agent terminal: it restarts the daemon (interrupting live
agent sessions, which auto-revive) and refuses if it detects it's running inside a daemon ConPTY
(override `-Force`). Verify: status bar reads `Protocol v5`. State (`~/.hangar/`) is global per
user, not per worktree.

---

## 5. Quirks, workarounds & gotchas  *(the high-value section — read before touching the daemon)*

- **Console-less daemon → flashing windows.** The daemon is spawned detached with no console, so **any
  console child it spawns without `CREATE_NO_WINDOW` flashes a window**. The diff is polled every ~2s
  (git per workspace), so a missed flag = constant flashing. **Always wrap `exec.Command` with
  `hideConsole(cmd)`** in the daemon (git, gh, where, run, taskkill). ConPTY sessions are headless and
  must **not** get the flag.
- **`detectPrompt` vs `detectWaiting`.** `detectPrompt` (narrow: only the agent's *approval* reject line)
  **drives AutoYes**, which taps Enter — widening it would auto-confirm selection menus. The status
  indicator uses the broader **`detectWaiting`** (approval + interactive footers like "esc to cancel",
  "to select"). Keep them separate.
- **Status heuristics.** `busy` = screen content changed within ~1.5s and not at a prompt; **input echo is
  ignored** (changes within 600ms of `sendKeys` don't count, else typing lit the spinner); **`waiting`
  takes priority over `busy`**. Freshness is bounded by the app's UI-refresh interval (Settings).
- **Programmatic prompt submission needs a focus-in, not just text + Enter.** Injecting a prompt into a
  live agent (Regenerate handoff/seed: `submitPrompt`, `workspace_windows.go`) and pressing Enter leaves
  it **sitting in the input box unsent** for focus-reporting CLIs (copilot): when the Regenerate
  modal/another pane takes focus, the desktop xterm sends the agent a **focus-out (`ESC[O`)**, after
  which it accepts typed text but won't submit until it sees a **focus-in (`ESC[I`)**. The fix sends
  `ESC[I` before submitting (the seed worked only because a just-booted agent never got a focus-out).
  The submit key is a bare `\r` — byte-identical to what xterm sends for a manual Enter, so the byte was
  never the issue. Text is typed via bracketed paste (`decModes[2004]`) when on, else in chunks; submit
  acceptance is detected only **after** the 600 ms input-echo window (busy or advanced `lastOutputUnixMs`).
  Knobs: `HANGAR_SUBMIT_FOCUS=0`, `HANGAR_SUBMIT_MODE`, `HANGAR_SUBMIT_ENTER`, `HANGAR_SUBMIT_SETTLE_MS`.
- **Stop the stale daemon after a Go rebuild.** The app reconnects to the *existing* daemon over the
  SID-named pipe, so a rebuilt `dist\cs.exe` won't be used until the old process is stopped
  (`Stop-Process -Id <pid>`; find it via `Get-CimInstance Win32_Process -Filter "Name='cs.exe'"`). The app
  **auto-reconnects** when the pipe drops (`ControlClient.isClosed()` + `cs:call` retry), so killing the
  daemon no longer strands the UI — but the **terminal** still needs a workspace re-click to re-attach.
- **Tests must isolate `HOME` AND `USERPROFILE`.** Windows `os.UserHomeDir()` reads `USERPROFILE`, not
  `HOME`. Early config tests set only `HOME` and **overwrote the real `~/.hangar/config.json`**
  (corrupting `default_program`). Fixed in `5171152`; any test touching config/worktrees must
  `t.Setenv` **both**.
- **Never `git worktree prune` from Windows.** The user has a WSL worktree whose path isn't visible to
  Windows git; a global prune would unregister it. Remove specific worktrees by path instead.
- **`desktop/go.mod` is a deliberate module boundary.** Some npm deps (e.g. `flatted`) ship stray `.go`
  files; without the nested module the parent's `go ./...` descends into `desktop/node_modules`. Don't
  delete it.
- **Agents launch via `cmd /c <program>`** so PATH lookup and `.cmd`/`.bat` shims (npm-installed copilot)
  resolve. `program` is space-split, so `copilot --session-id=<uuid>` works as one program string.
- **Resume is copilot-only.** `agentLaunchCommand` appends `--session-id=<uuid>` only for copilot (verified
  flag: sets the UUID for a new session **or** resumes it). Other agents and **workspaces created before
  the feature** (no stored `AgentSessionID`) fall back to a fresh start. Add other agents only after
  verifying their resume flag.
- **Shell (Terminal tab) sessions are ephemeral.** `sh_<wsId>` is created via the generic `CreateSession`,
  kept alive while the app runs (re-open re-attaches the same shell), and killed on **archive + app quit**.
  They are **not** persisted/revived across a daemon restart (only the agent conversation resumes).
- **Invisible panics → logs.** The detached daemon's stderr is discarded; panics were silent. Recovered
  panics now go to **`~/.hangar/host.log`**; the app logs to **`~/.hangar/desktop.log`**
  (incl. uncaught exceptions and updater activity). Check these first when debugging.
- **npm-audit advisories.** `react-diff-view` (runtime, dev-flagged) and `electron-builder` (build-time)
  add advisories. Review before any public release; they are not shipped in the app runtime path.
- **One host per user.** SID-named singleton pipe `\\.\pipe\hangar-host-<SID>`; the TUI and the app
  share it. `host.lock` is an OS lock released on process exit (a stale lock doesn't block relaunch).

---

## 6. On-disk state (`~/.hangar/`, i.e. `%USERPROFILE%\.hangar`)

| Path | Owner | Contents |
|---|---|---|
| `config.json` | daemon | `default_program`, `auto_yes`, `daemon_poll_interval`, `branch_prefix`. **Don't corrupt** (see tests gotcha). |
| `desktop.json` | app | `notifications`, `minimizeToTray`, `uiRefreshMs` (kept separate so daemon rewrites don't drop them). |
| `workspaces.json` | daemon | Workspace metadata incl. `branch`, `worktreePath`, `sessionName`, `baseSHA`, `runCommand`, **`agentSessionId`**. |
| `worktrees/` | daemon | The per-workspace git worktrees. |
| `host.json` / `host.lock` / `host.log` | daemon | Pipe discovery / singleton lock / daemon log (incl. recovered panics). |
| `state.json` | TUI | Legacy bubbletea TUI state. |
| `~/.copilot/` | copilot CLI | Per-session conversation state keyed by UUID — what `--session-id` resume reads. |

---

## 7. Outstanding / next work

- **E3 PR/merge/checks** — Create-PR, merge, archive-on-merge, CI checks. Needs `gh` auth (the token was
  invalid last session) and the GitHub API; deferred by user choice. Daemon has commit/push already.
- **Terminal auto-reattach after a daemon restart** — currently you re-click the workspace to trigger the
  resuming revive; a smoother auto-reattach is a nicety.
- **Per-workspace "needs input" notifications** — needs the daemon to surface side-effect-free prompt
  state per workspace (today only "agent finished" fires). `detectWaiting` already exists to build on.
- **Multi-window pop-out** (E5 deferred).
- **E6 signing & updater** — the installer is **unsigned** (no EV cert); set `CSC_LINK`/`CSC_KEY_PASSWORD`
  or Azure Trusted Signing (`PACKAGING.md`). The updater `publish` owner/repo should point to `thirschel/Hangar`
  before shipping.
- **Files tab editing** — read-only this round; a Monaco editor with save is the natural next step.
- **Resume for non-copilot agents** — verify claude/codex/aider/gemini resume flags, then extend
  `supportsResume`/`agentLaunchCommand`.
- **Run config** — currently a per-workspace command; a project-level `.cs/settings.json`
  (cf. `.conductor/settings.toml`) is a later refinement.
- **npm-audit cleanup** before public release.
- **Land the branch** — `desktop-core-daemon` is not yet PR'd to the fork's default branch.

---

## 8. Conventions

- **Commits:** end the message with `Co-authored-by: Copilot <223556219+Copilot@users.noreply.github.com>`.
- **Branch:** `desktop-core-daemon`. **Fork only** — never push to / PR against upstream `smtg-ai/claude-squad`.
- **Every change:** `go build/test ./...` green on **Windows AND Linux**, plus app `lint` + `typecheck` +
  `build`. `gofmt` Go files (explicit paths). After a Go change, rebuild `dist\cs.exe` and stop the stale
  daemon.
- **Don't break** the Unix/tmux path or the bubbletea TUI — new Go is Windows-host-side or additive RPC.
- **Comments:** only where they clarify non-obvious intent (the codebase favors explanatory comments on
  the tricky concurrency/Windows bits).
