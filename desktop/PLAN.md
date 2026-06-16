# claude-squad Desktop (Electron, Windows) — Phased Implementation Plan

> Builds on `files/electron-feasibility.md` (architecture + parity) and the working POC in this
> directory. Goal: a Conductor-like (www.conductor.build) desktop app — run **parallel coding agents
> in isolated git-worktree workspaces**, each with its own agent terminal, diff, and review→PR→merge
> path. Windows-first.

---

## 1. Vision & Conductor mapping

Conductor: *"Each task gets its own workspace, branch, files, terminal, diff, and review path. When
the work is ready, review the diff, open a PR, merge, and archive the workspace."* That is exactly
claude-squad's worktree-per-session model, with a richer GUI around it.

| Conductor concept | claude-squad equivalent |
|---|---|
| Workspace (isolated, own branch/files/terminal/diff) | Instance = git worktree + agent terminal session (exists) |
| Parallel agents | Multiple host sessions (exists) |
| "See at a glance what they're working on" | Workspace list w/ live status + diff stats (left sidebar) |
| Agent conversation (center) | The agent's live **xterm.js terminal** + a styled composer |
| Diff viewer / Changes / Review (right) | Per-workspace changed files + diff (exists in Go; expose via RPC) |
| Create PR / PR #/ Ready for review / Checks | New: `gh`/GitHub API integration (later phase) |
| Setup/Run + live preview (`.conductor/settings.toml`) | New: per-project run command + preview panel (later phase) |
| Archive workspace | Kill session + remove worktree (exists: Pause/Kill) |

---

## 2. Locked decisions (this session)

- **MVP (M1) = core shell:** open a repo → create/list/archive parallel workspaces → agent terminal
  in the center → basic changed-files/diff panel. (Review/PR, run/preview, native polish are later.)
- **Center pane = terminal-as-conversation:** the agent's live xterm.js terminal *is* the center,
  with Conductor-style chrome (tabs + a composer that sends keystrokes). Works with any terminal
  agent (copilot/claude/codex/aider/gemini). No fragile output-parsing.
- **Architecture = Electron front-end + extend the Go session-host into a "core daemon"** (the Wave
  Terminal model). Keep PTYs/persistence/AutoYes in Go; Electron renders the attach stream in
  xterm.js and calls RPC. Do **not** reimplement orchestration in node-pty.
- **Windows-first.** Unix/tmux and the existing bubbletea TUI stay working and unchanged.

## 3. Open decisions (please confirm/adjust when reviewing)

- **D1 — Repo layout:** monorepo — add the Electron app as `desktop/` inside the `claude-squad`
  repo (bundles the same `cs.exe` daemon; one protocol version). *Recommended* (evolve this POC into
  `desktop/`). Alternative: a separate repo.
- **D2 — UI stack:** Electron + **TypeScript + React + Vite** (largest ecosystem; HMR). Alternative:
  Svelte/Vue. *Recommended: React + TS + Vite.*
- **D3 — Primary agent for testing:** `copilot` (your daily driver). Others are just profiles.
- **D4 — Bubbletea TUI on Windows:** keep it during the transition; retire it on Windows once the
  Electron app reaches parity (Unix keeps it permanently).

---

## 4. Architecture

```
  Electron app (desktop/, Windows)                         core daemon = cs.exe (Go, extended host)
  ┌───────────────────────────────┐                        ┌─────────────────────────────────────────┐
  │ renderer (React + xterm.js)   │  preload  ipcMain      │ control pipe  \\.\pipe\claudesquad-host-* │
  │  - workspace sidebar          │◄────────►│  host-client│   length-prefixed JSON RPC (extended)     │
  │  - agent terminal + composer  │          │  (TS, from  │ ── owns Workspaces (instance.go + state)  │
  │  - changes/diff panel         │          │   proto.go) │ ── owns git worktrees (session/git)       │
  └───────────────────────────────┘          └─────────────│ ── owns ConPTY sessions + x/vt emulator   │
            ▲ attach stream (raw VT bytes) ─────────────────│ ── host-side AutoYes, persistence         │
            └──────────────── per-session attach pipe ──────│ ── (later) diff/PR/run-preview RPC        │
                                                            └─────────────────────────────────────────┘
```

**Core daemon (the key backend change).** Today the host owns *terminals only*; the bubbletea TUI
owns workspaces/worktrees/diff/config. We **promote the host to own the whole workspace lifecycle**
by invoking the existing, already-written Go packages from inside the host:
`session/instance.go` (lifecycle), `session/git/` (worktrees + diff), `session/storage.go` +
`state.json` (persistence), `config/` (profiles). This is a **facade over existing logic**, not a
rewrite. The daemon becomes the single source of truth; both the Electron app and (optionally) the
Windows TUI become thin clients.

**Frontend.** Reuse the proven POC `protocol.js` (→ typed `host-client.ts`): SID-derived control
pipe, length-prefixed JSON RPC, and the token-frame→raw-stream attach. The main process holds the
client; the renderer talks to it via a typed preload bridge.

**Terminal-as-conversation.** Each workspace = one host session. The selected workspace is
**attached** (xterm.js fed by its attach stream; host sends an emulator snapshot then live bytes).
Keystrokes (xterm `onData`) and the composer both write to the **same** attach input channel (the
host serializes input, so no interleave). Switching workspaces re-attaches (a fresh snapshot
repaints instantly because the host always-drains, so the emulator is current). Active terminal uses
the **WebGL** renderer; background ones use **DOM**/`@xterm/headless` to stay under Chromium's ~16
WebGL-context limit.

---

## 5. UI design (three-pane, Conductor-like)

```
┌── top bar ───────────────────────────────────────────────────────────────────────┐
│ [≡] Project ▸ Workspace ▸ …            [branch]   [PR #/status]*    [Create PR]*    │
├───────────────┬──────────────────────────────────────┬─────────────────────────────┤
│ SIDEBAR       │ CENTER (agent workspace)             │ RIGHT (review)              │
│ Projects ▾    │ ┌ tabs: Agent | Files* | Terminal* ┐ │ tabs: Changes | Diff |      │
│ Workspaces +  │ │                                  │ │       Checks* | Review*     │
│  ● 1. feat-x  │ │   xterm.js (agent TUI)           │ │ src/App.tsx     +12         │
│  ● 2. bugfix  │ │                                  │ │ src/App.css     +31         │
│  ⏸ 3. spike   │ │                                  │ │ ── selected file diff ──    │
│               │ ├──────────────────────────────────┤ │                             │
│               │ │ composer: "Add a follow-up…" [▸] │ │ Run/Preview*  [Open][Stop]  │
│ History*      │ │ [agent ▾] [AutoYes ⊙]            │ │                             │
└───────────────┴──────────────────────────────────────┴─────────────────────────────┘
   (* = later phase)
```

**Components (M1):**
- `Sidebar` — project switcher; workspace list (status dot ●/⏸/spinner, title, branch, +N/-N diff
  stats); `+` create; right-click/hover → archive.
- `CenterPane` — tab bar; `AgentTerminal` (xterm.js + fit/webgl) bound to the selected workspace;
  `Composer` (auto-grow textarea; Enter=send, Shift+Enter=newline; send → attach input); an `AutoYes`
  toggle.
- `ReviewPanel` — `ChangesTab` (changed files + counts; click → diff); `DiffView` (read-only v1).
- `TopBar` — breadcrumb, window controls, status (daemon connected / version).
- `StatusBar` — errors, daemon state.

**Theme:** dark, VS Code-like; Segoe UI + a mono font for terminals; match Conductor's calm, flat
look.

---

## 6. Tech stack

- **Electron** (latest stable) + **TypeScript**.
- **Renderer:** React + Vite; state via Zustand (small) or React context; routing not needed (panes).
- **Terminal:** `@xterm/xterm` + `@xterm/addon-fit`, `@xterm/addon-webgl`, `@xterm/addon-serialize`
  (previews), `@xterm/addon-search`. Pass `windowsPty:{backend:'conpty',buildNumber}`.
- **Diff:** v1 `react-diff-view`/`diff2html`; later **Monaco diff editor**.
- **IPC client:** TS port of the POC `protocol.js`; typed RPC + attach stream.
- **Tooling:** ESLint + Prettier + tsc; `electron-vite` or Vite + electron-builder.
- **Packaging (later):** electron-builder (NSIS) + electron-updater; EV code-signing.

---

## 7. Phases, items & acceptance criteria

> **M1 (the chosen MVP) = E0 + E1 + E2.** E3–E6 are the roadmap beyond MVP.

### E0 — Scaffold & typed protocol (frontend foundation)
**Items**
- Scaffold `desktop/` (or evolve this POC): Electron + TS + React + Vite; main/preload/renderer; CSP;
  ESLint/Prettier/tsc; dev + build scripts.
- Port `protocol.js` → `host-client.ts`: typed `Request`/`Response` mirrored from `proto.go`,
  `FrameDecoder`, `ControlClient`, attach client, `ensureHost` (SID pipe; bundled-daemon path).
- Typed preload bridge (`window.cs`): `call(rpc)`, `attach(workspaceId)`, `onData`, `sendInput`,
  `resize`, events.
- App-shell skeleton: three-pane layout, top/status bars, dark theme (empty panels OK).

**Acceptance**
- `npm run dev` opens the app with the three-pane shell; `npm run build` produces an app.
- App connects to the daemon: top bar shows "connected · host vN"; a dev "ping" creates+kills a
  throwaway session (the POC selftest, ported to TS) and passes.
- `tsc`, ESLint clean.

### E1 — Core daemon: workspace RPC (Go) `[biggest backend lift]`
**Items**
- Extend `proto.go` (bump `Version`) with workspace methods (names illustrative):
  `ListWorkspaces`, `CreateWorkspace{repoPath,title,program,baseBranch?}`, `GetWorkspace{id}`,
  `ArchiveWorkspace{id}`, `RenameWorkspace{id,title}`, `WorkspaceDiff{id,file?}` (changed files +
  per-file unified diff), `SetWorkspaceAutoYes{id,enabled}`.
- Make the host own the workspace registry + persistence: drive `session/instance.go`,
  `session/git/`, `session/storage.go` (`state.json`), `config/` from inside the host process
  (reuse, don't rewrite). Create = worktree+branch + agent session + persist; Archive = kill session
  + remove worktree/branch (reuse `Instance.Kill`/`Pause`).
- Map workspace → its terminal session so `Attach`/`SendKeys`/`Resize`/`CapturePane` keep working.
- Diff via existing `Instance.ComputeDiff` / `git` numstat + unified diff.

**Acceptance**
- Headless test (Go or TS, in a temp git repo): `CreateWorkspace` → a worktree+branch exist and an
  agent session starts; `ListWorkspaces` reports it with live status + diff stats; after the agent
  edits a file, `WorkspaceDiff` returns the changed file with correct +/- and a unified diff;
  `ArchiveWorkspace` removes the worktree+branch and the session.
- Persistence: restart the daemon → `ListWorkspaces` still returns live/recreatable workspaces.
- **No regressions:** `go test ./...` green on Windows **and** Linux; the bubbletea TUI still runs;
  version-skew path still handled.

### E2 — Electron core shell (**M1 ships here**)
**Items**
- Sidebar: `ListWorkspaces` (live poll/events), create dialog (title, agent/profile, base branch),
  archive (confirm), status dots + diff stats, selection.
- Center Agent tab: xterm.js bound to the selected workspace's attach stream; switch = re-attach
  (snapshot repaint); composer → attach input; xterm input → attach input; AutoYes toggle
  (`SetWorkspaceAutoYes`). WebGL for active, DOM for the rest.
- Right Changes tab: changed files + counts (`WorkspaceDiff`), click → read-only diff; refresh on a
  tick/on demand.
- Top bar breadcrumb + window chrome; persistence across app restart.

**Acceptance (MVP "done")**
- Open a repo; create **2+ parallel workspaces**; each shows its agent's **live terminal**; typing
  (composer or terminal) drives the agent; **switching workspaces is instant** and preserves each
  terminal's state.
- The Changes panel shows the agent's edits (+/- counts) and a viewable diff.
- **Archive** cleans worktree/branch/session.
- **Close & reopen the app** → workspaces persist (daemon kept them alive) and reattach; no stolen
  input, no hangs, resize reflows; N workspaces stay under the WebGL limit.

### E3 — Review & PR workflow
**Items:** Monaco diff viewer; stage/commit controls; **Create PR** (gh/GitHub API); PR badge +
"Ready for review"; **merge**; archive-on-merge; **Checks** tab (CI status via API).
**Acceptance:** from a workspace, review the diff, open a real PR into the repo, see PR + checks
status, merge, and the workspace auto-archives.

### E4 — Run & preview panel
**Items:** per-project setup/run config (a `cs`-namespaced settings file; cf. `.conductor/settings.toml`);
daemon-managed run process per worktree; Run panel streaming output; **preview URL** detection +
"Open"/"Stop".
**Acceptance:** define a run command; start it per workspace; see live output + a clickable preview
URL; stop it; isolated per worktree.

### E5 — Native shell & polish
**Items:** system tray (status + quick switch); **global hotkeys** (summon/jump); native
notifications (agent needs input / done / checks failed); multi-window (pop out a workspace);
settings UI (profiles/agents/theme); full keyboard-shortcut parity.
**Acceptance:** tray + global hotkey work; notifications fire on prompt-waiting/agent-done; settings
persist; AutoYes/shortcuts behave.

### E6 — Packaging & distribution
**Items:** electron-builder NSIS installer bundling the **signed** core-daemon binary;
electron-updater auto-update; EV code-signing + SmartScreen handling; crash/log reporting.
**Acceptance:** a signed installer; clean install on a fresh machine runs; auto-update ships a new
version; the bundled daemon launches and connects.

---

## 8. Cross-cutting concerns

- **Don't break Unix/tmux or the existing TUI.** All new Go is Windows-host-side or additive RPC.
  Gate everything; keep `go test ./...` green on both platforms each phase.
- **Protocol versioning.** Bump `proto.Version` when adding workspace RPC; the existing
  `VersionMismatch`/`AsVersionMismatch` path handles old-host/new-client gracefully (reuse for the
  Electron app's auto-update story).
- **Single source of truth = the daemon.** The Electron app holds no durable state; restart-safety
  comes for free.
- **One host per user (singleton).** The Electron app connects to the same SID-named pipe as the TUI;
  they can coexist during the transition.
- **Testing.** Keep the headless seam/selftest style (no GUI) for daemon + protocol; add Playwright/
  Spectron-style smoke tests for the renderer later.

## 9. Risks & mitigations (from the feasibility research)

| Risk | Mitigation |
|---|---|
| Electron Job Object kills the daemon on exit | Daemon self-detaches (`DETACHED_PROCESS|CREATE_NEW_PROCESS_GROUP`); Electron just launches it. |
| Named-pipe framing/partial reads | Length-prefix state-machine decoder (done in the POC); fuzz chunk splits. |
| WebGL context limit w/ many terminals | Active=WebGL, inactive=DOM/`@xterm/headless`. |
| Auto-update protocol skew (new UI / old daemon) | Reuse `VersionMismatch`; prompt + restart daemon on next idle; never force-kill live sessions. |
| SmartScreen on the signed daemon | EV cert; document first run. |
| Lifting orchestration into the daemon touches shared Go | Additive RPC; keep TUI path working; full test matrix each phase. |
| Two input paths (composer vs xterm) | Route both through the attach input channel; host serializes writes. |

## 10. Suggested execution order

`E0 ∥ E1` can proceed in parallel (frontend scaffold vs Go RPC), converging at **E2** for the MVP.
Then E3 → E4 → E5 → E6. E1 is the critical path; start there + E0 together.
