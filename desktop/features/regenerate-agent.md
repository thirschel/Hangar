# Feature: Regenerate Agent

> **Status:** Reviewed (v2) — incorporates independent reviews by **GPT-5.5**, **Gemini 3.1 Pro**, and **Claude Opus 4.8**.
> **Area:** Desktop app (`desktop/`) + Go core daemon (`session/winhost/`)
> **Branch:** `desktop-core-daemon` (fork only — see `desktop/HANDOFF.md`)
> **Proto impact:** bumps wire protocol v4 → **v5** (new RPC methods + additive `WorkspaceInfo` fields)

## 1. Summary

Add a **Regenerate Agent** action to a workspace. When an agent's conversation has been
compacted, polluted, or led down a misleading path, the user wants to kill the current agent
session and start a **fresh** one — *without* throwing away the accumulated context.

Clicking **Regenerate** (a button next to **AutoYes** in the agent pane) opens a confirmation
popup that:

1. Confirms the user wants to **kill the current agent** for this workspace, and
2. Offers a **checkbox** to *"Create a handoff document and seed the new agent"* (on by default).

With the handoff option on, the app asks the **current, live agent** (which still holds the full
conversation context) to write a `HANDOFF.md` describing what it was doing, the current status,
next steps, and relevant details. Once the handoff is detected complete, the app replaces the
session with a **fresh agent** in the same worktree/branch and **seeds** it to read `HANDOFF.md`
and continue. The handoff path costs time and tokens, so it is **opt-in per click**, with a manual
**Kill now** escape hatch and a deterministic fallback that guarantees the new agent always gets
*some* context.

## 2. Confirmed decisions

| # | Decision | Choice |
|---|---|---|
| D1 | Feature-doc location | `desktop/features/regenerate-agent.md` (this file) |
| D2 | Review models | GPT-5.5, Gemini 3.1 Pro, Claude Opus 4.8 (independent rubber-duck reviews — see §3) |
| D3 | Handoff-completion detection | **Hybrid**: auto-detect (file written + agent idle) **plus** a manual **Kill now** fallback and a deterministic transcript fallback |
| D4 | Agent scope | **Copilot-first**: fully supported for copilot; **degrade gracefully** for other agents |
| D5 | Context | **Fresh, not resume** — rotate `AgentSessionID` to a never-used UUID; never resume the old conversation |
| D6 | Workspace identity | Regenerate replaces **only the agent session**; worktree, branch, working-tree changes, run command, and AutoYes are preserved |
| D7 | Handoff file | `HANDOFF.md` in the worktree root (the agent's cwd); **not** auto-committed; overwritten each regenerate; visible in Files/Changes |
| D8 | Prompts | Hardcoded constants for MVP; configurable later |
| D9 | Session swap | **Rotate `SessionName`** (transactional restart). This makes the desktop terminal re-attach automatically and avoids the duplicate-name guard. (Review-driven; see §3, §9.3.) |
| D10 | Timeout model | **Inactivity-based** (agent idle + file unchanged), with an absolute hard cap — not a flat wall-clock kill. (Review-driven; see §9.4.) |

## 3. Multi-model review summary

All three reviewers returned **"Needs changes"** — no restructuring required, but several
correctness gaps had to be closed. The strongest, **independently-corroborated** findings (now
addressed in v2):

| Finding | Raised by | Resolution in v2 |
|---|---|---|
| **Terminal won't re-attach** — reusing `SessionName` leaves the UI stuck at `[session ended]` because `TermView` only attaches on `sessionName` change (`TermView.tsx:31–140`) and dropped sockets aren't auto-reconnected | GPT-5.5 (B1), self-verified | **Rotate `SessionName`** (D9) → React remounts `TermView` → auto re-attach (§9.3, §9.8) |
| **Archive-during-regenerate** TOCTOU can spawn a zombie ConPTY in a just-removed worktree | all three (B1/B1/R8) | `archive` cancels the regen, sets a tombstone, and waits on the goroutine's `done`; `restartAgent` re-checks existence as the last step under `m.mu` (§9.3, §9.5) |
| **FR-10 not actually guaranteed** — kill-before-start strands the workspace if the new start fails; a panic mid-window leaves it dead with the button stuck | GPT-5.5 (B3), Opus (B2) | Persist rotated id/name **before** kill; bounded retry; **revivable-on-attach** fallback; `defer` clears `regens[id]` and attempts a final revive (§9.3); FR-10 reworded (§6) |
| **Completion detection** ignored `waiting`; flat timeout truncates slow writes | all three | `handoffReady` considers `busy` **and** `waiting`, uses an **inactivity** timeout + hard cap, content-hash baseline, optional sentinel (§9.4) |
| **Spurious "Agent finished" notification** fires mid-regenerate (alive→dead) | GPT-5.5 (M6), Opus (M1) | `Regenerating=true` for the **whole** op incl. fast path; suppress alive→dead notify while regenerating (§9.8) |
| **Force-approve** must bypass both the `attached` **and** AutoYes-`enabled` gates; cap exposure | all three (R1/R2) | `setForceApprove` bypasses both gates; disengage the instant completion fires (no hard 1-cap — read-then-write may need 2) (§9.6) |
| **Seeding/handoff-send timing** — input dropped if sent before the agent is ready | all three (R4) | Wait for the agent's idle/prompt signal (+floor delay) before sending the handoff prompt and the seed; injectable; manual-QA-verified (§9.6, §9.7) |
| **Test reality** — with `fakeSession`, `agentStatus` is always idle and `sendKeys` approves nothing, so timing-sensitive paths are trivially satisfied | Opus, GPT-5.5 (M4) | Lean on the **pure `handoffReady` table test**; extend `fakeSession` to simulate busy/waiting + a scripted file write; unit-test id rotation by injecting a workspace directly (CreateWorkspace's `LookPath` blocks a `copilot` integration test) (§11) |
| **Deterministic fallback** — seeding an empty/partial file wastes the regenerate | Opus (alternative) | On timeout/Kill-now with no usable handoff, the daemon writes `HANDOFF.md` from `capture(full)` scrollback — no agent cooperation needed (§9.9) |

Notable **disagreement**: GPT-5.5 wanted a required completion **sentinel**; Gemini and Opus judged a
required sentinel too prompt-fragile. **Resolution:** accept a sentinel as an *optional early* signal,
keep the file detector primary (§9.4).

**Confirmed correct by review:** the async-goroutine+poll pattern (mirrors `generateTitle`), the
`managedSession`/`fakeSession` test seam, the fresh-context rotation (`copilot --session-id=<newUUID>`
has no `~/.copilot/<uuid>` history → fresh), the `m.mu → h.mu` lock order, and the v5 version gate.

## 4. User stories

- *As a user whose agent's context got compacted,* I want a fresh agent that still knows what the
  old one was doing, so I don't lose progress.
- *As a user whose agent went down a wrong path,* I want a clean restart in the same worktree
  without re-creating the workspace or re-explaining everything.
- *As a token-conscious user,* I want to skip the handoff and just restart instantly.
- *As an impatient user,* I want to stop waiting on a slow handoff and kill the agent immediately,
  still keeping whatever context can be salvaged.

## 5. UX / behavior

### 5.1 Entry point — the button
- A **Regenerate** button (`↻ Regenerate`) is added to the agent-pane tab-bar in `CenterPane.tsx`,
  immediately **next to the AutoYes checkbox**. Visible only with a workspace selected; disabled
  while `workspace.regenerating === true`.

### 5.2 The confirmation popup (`RegenerateModal`)
- **Title:** "Regenerate agent?"
- **Body:** "This kills the current agent for **{title}** and starts a fresh one in the same
  worktree and branch. Your files and changes are kept."
- **Checkbox** (default **checked**): "Create a handoff document (`HANDOFF.md`) and seed the new
  agent with it." Helper: "Preserves context. Takes longer and uses tokens. Overwrites any existing
  HANDOFF.md."
- **Primary:** "Regenerate". **Secondary:** "Cancel" (no changes).

### 5.3 Progress + manual fallback
While a handoff regenerate runs, the modal reflects the daemon-reported phase:

| Phase (`regenPhase`) | UI copy | Controls |
|---|---|---|
| `handoff` | "Asking the agent to write HANDOFF.md…" | **Kill now** |
| `restarting` | "Restarting the agent…" | — |
| `seeding` | "Seeding the new agent with the handoff…" | — |
| (cleared) | closes; optional "Agent regenerated" notification | — |

- **Kill now** → `ForceRegenerate`: stop waiting and proceed immediately, salvaging context via the
  transcript fallback (§9.9) if the agent hasn't produced a usable handoff.
- The non-handoff (fast) path skips straight to `restarting`.

## 6. Functional requirements

1. **FR-1** A Regenerate control exists next to AutoYes for the selected workspace.
2. **FR-2** Clicking it opens a confirmation popup with a handoff checkbox (default on).
3. **FR-3** Confirm with handoff **off** → the agent is replaced by a fresh agent in the same
   worktree/branch; no handoff written; no seed sent.
4. **FR-4** Confirm with handoff **on** → the live agent is asked to write `HANDOFF.md`; once
   detected complete (or on timeout / Kill now), the session is replaced and the new agent is seeded
   to read `HANDOFF.md` and continue.
5. **FR-5** Regenerate **rotates** the workspace's `AgentSessionID` (copilot) so the new agent starts
   a **fresh** conversation, never resuming the old one.
6. **FR-6** Regenerate preserves the worktree, branch, working-tree changes, run command, and AutoYes
   setting; **only the agent session** (and its `SessionName`) is replaced.
7. **FR-7** A **Kill now** action short-circuits the handoff wait at any time.
8. **FR-8** `regenerating` (+ phase) is surfaced for the **whole** operation (both paths) so the UI
   shows progress and prevents a concurrent regenerate on the same workspace.
9. **FR-9** **Copilot** is fully supported. Non-copilot agents still regenerate (fresh start + handoff
   via files/messages); session-id rotation is a no-op for agents without a stable id.
10. **FR-10** A regenerate must never leave a workspace with a **stale or duplicate** session, and
    must always leave it **live or revivable-on-next-attach** — even on start failure, timeout, or
    panic. (Reworded per review: the system can guarantee *revivable*, not *instantaneously live*.)
11. **FR-11** After a regenerate completes, the agent terminal **auto-re-attaches** and accepts input
    without a manual reselect.
12. **FR-12** A regenerate must not emit a false **"Agent finished"** notification.

## 7. Non-goals

- No change to the Unix/tmux TUI or bubbletea path (Windows-host / additive RPC only).
- No automatic commit/push of `HANDOFF.md`.
- No workspace "fork"/clone — this replaces in place.
- No user-configurable prompts in the MVP (D8).
- No resume of the old conversation (that is the existing copilot `--session-id` behavior,
  intentionally **not** used here).

## 8. Architecture recap

Thin Electron client → `ipcMain 'cs:call'` → `ControlClient` (pipe) → daemon `dispatch()` →
`workspaceManager`. A *workspace* = git worktree + branch + an agent ConPTY session owned by the
daemon. Precedents this feature reuses:

- `workspaceManager.generateTitle` (`workspace_windows.go:175–204`) — async agent task in a goroutine;
  UI polls for the result. **The handoff path mirrors this.**
- `agentLaunchCommand` / `supportsResume` / `newUUID` (`workspace_windows.go:119–147`) — copilot
  session-id handling. Regenerate **rotates** the id.
- `host.startManagedSession` / `host.killSession` (`host_windows.go:286–330`) — create/destroy a
  session. **Note the duplicate-name guard** (`host_windows.go:289–291`) → drives `SessionName`
  rotation (D9).
- `conptySession.sendKeys` / `agentStatus()` / `maybeAutoYes` / `detectPrompt` (`conpty_windows.go`)
  — send prompts, detect idle, host-side approval. **`maybeAutoYes` pauses while attached and while
  AutoYes is off** → drives `setForceApprove` (§9.6).
- `reviveBySession` (`workspace_windows.go:351–377`) — relaunch a workspace's session from persisted
  metadata; the revivable-on-attach fallback for FR-10.
- `managedSession` interface + `fakeSession` (`host_windows.go:33`, `host_windows_test.go:19–72`) — the
  test seam (`host.newSession`).

## 9. Detailed design

### 9.1 Protocol (`session/winhost/proto/proto.go`) → v5
- `Version = 5`.
- Methods: `MethodRegenerateAgent = "RegenerateAgent"`, `MethodForceRegenerate = "ForceRegenerate"`.
- `Request` additions: `Handoff bool` (create+seed; single coupled flag). Reuse existing `Cols`/`Rows`
  so the client passes its current terminal size into the restart (avoids a mis-sized PTY).
- `WorkspaceInfo` additions (additive): `Regenerating bool`, `RegenPhase string`
  (`""|handoff|restarting|seeding`).

### 9.2 Dispatch (`host_windows.go`)
```go
case proto.MethodRegenerateAgent: return h.workspaces.regenerate(req)
case proto.MethodForceRegenerate: return h.workspaces.forceRegenerate(req)
```

### 9.3 Regen orchestration (`workspace_windows.go`)

**In-memory state** (never persisted):
```go
type regenState struct {
    phase     string        // "handoff" | "restarting" | "seeding"
    force     chan struct{} // closed by ForceRegenerate / Kill now
    done      chan struct{} // closed when the goroutine exits (archive waits on this)
    closeOnce sync.Once     // guards close(force)
}
// on workspaceManager:
regens    map[string]*regenState // by workspace ID, guarded by m.mu
tombstone map[string]bool        // workspace IDs archived mid-regen, guarded by m.mu
```
> All `regenState` field reads/writes happen in short critical sections under `m.mu` (snapshot →
> unlock → act → relock to advance `phase`) to stay race-free under `-race` (Opus M3).

**`regenerate(req)`** — registers `regens[id]` and sets `Regenerating=true` for **both** paths
(fast + handoff) so FR-8/FR-12 cover the whole window:
- Look up workspace (error if missing). If `regens[id]` already present → soft error (no double-start).
- **Fast path (`!req.Handoff`)**: `go m.runRegenerate(id, req.Cols, req.Rows, false)`; return OK.
- **Handoff path**: record `HANDOFF.md` **content-hash baseline**; wait for the agent to be `!busy`
  (bounded) then engage `setForceApprove` and `sendKeys(handoffPrompt+"\r")`; `go m.runRegenerate(id,
  cols, rows, true)`; return OK (async, like `generateTitle`).

**`runRegenerate(id, cols, rows, handoff)`** goroutine — `defer recoverGoroutine(...)`,
`defer close(regen.done)`, `defer clear regens[id]`, `defer disengage force-approve`:
- If `handoff`: ticker (~500 ms) computing a `regenWait` snapshot → `handoffReady` (§9.4); also
  `select` on `regen.force`. On proceed with no usable handoff → write the transcript fallback (§9.9).
- Then `restartAgent(id, cols, rows)` (phase→`restarting`); if `handoff`, phase→`seeding`, wait for
  the new agent ready, `sendKeys(seedPrompt+"\r")`.
- **FR-10 guarantee:** the `defer` clears `regens[id]` always; if the body unwinds between kill and a
  successful start, it persists the rotated name/id (so the next attach revives the **fresh**
  session) and attempts one final start.

**`restartAgent(id, cols, rows)`** (snapshot-then-unlock; matches `reviveBySession`):
```
lock m.mu
  if tombstone[id] || wss[id]==nil { unlock; return errArchived }   // B1/B2 guard
  w := wss[id]
  oldName := w.SessionName
  w.SessionName = "ws_" + id + "-" + shortRand()    // ROTATE (D9) -> UI re-attaches
  if supportsResume(w.Program) { w.AgentSessionID = newUUID() } else { w.AgentSessionID = "" }
  snapshot newName, program, worktreePath, autoYes
  saveLocked()                                       // persist BEFORE kill -> revivable
unlock
killSession(oldName)                                 // do NOT stop runs.* (worktree-scoped, keep running)
for attempt in 0..N:
  if startManagedSession(newName, agentLaunchCommand(program, id), worktreePath, cols|120, rows|30, autoYes) == nil:
     return nil
return err   // leave revivable: metadata already persisted with newName -> reviveBySession recovers
```
> `runs.stop` is **omitted** on purpose: the run process is the user's dev server, keyed by workspace
> ID and bound to the worktree (not the agent) — killing it would surprise the user (GPT-5.5 minor).

**`forceRegenerate(req)`**: look up `regens[id]`; `regen.closeOnce.Do(func(){ close(regen.force) })`;
return OK. No active regen → soft no-op.

**`archive(req)`** (augment existing): under `m.mu`, if `regens[id]` exists, set `tombstone[id]=true`
and `closeOnce`-close `force`; capture `done`; release `m.mu`; **wait on `done`** (bounded) **before**
`wt.Remove()`, so no session can be started into a removed worktree (B1).

**`toInfo`**: set `Regenerating`/`RegenPhase` from `regens[w.ID]` (read under `m.mu`).

### 9.4 Pure completion-decision function (the testable core)
```go
type regenThresholds struct{ stableMs, graceMs, inactivityMs, hardCapMs int64 }

type regenWait struct {
    sentinelSeen bool  // optional: agent printed the completion sentinel
    fileChanged  bool  // HANDOFF.md content HASH differs from the pre-prompt baseline
    fileStableMs int64 // ms since the file content last changed
    agentBusy    bool  // agentStatus().busy
    agentWaiting bool  // agentStatus().waiting (blocked at a prompt/menu)
    inactiveMs   int64 // ms since the agent was last busy AND the file last changed
    elapsedMs    int64 // ms since the handoff prompt was sent
    forced       bool  // Kill now
}

// handoffReady reports whether to proceed to kill+recreate, and why (for the
// transcript-fallback decision and logging).
func handoffReady(s regenWait, th regenThresholds) (proceed bool, reason string) {
    switch {
    case s.forced:
        return true, "forced"
    case s.sentinelSeen && s.fileChanged:
        return true, "sentinel"
    case s.fileChanged && s.fileStableMs >= th.stableMs && !s.agentBusy && s.elapsedMs >= th.graceMs:
        return true, "file-stable-idle"           // agentWaiting OK here: the write is done
    case s.inactiveMs >= th.inactivityMs:
        return true, "inactivity"                 // idle + no writes -> agent gave up / refused
    case s.elapsedMs >= th.hardCapMs:
        return true, "hardcap"
    default:
        return false, ""
    }
}
```
- Defaults (injectable for tests): `stableMs≈1500`, `graceMs≈4000`, `inactivityMs≈30000`,
  `hardCapMs≈300000` (5 min). The **inactivity** timer (not a flat wall-clock) prevents killing a
  healthy-but-slow handoff mid-write (Opus M2/R9).
- **Content-hash** baseline (not mtime/size) so a same-length re-regenerate isn't missed (Opus minor).
- `reason ∈ {forced, inactivity, hardcap}` with `!fileChanged` triggers the transcript fallback (§9.9).

### 9.5 Concurrency / teardown safety
- Lock order **`m.mu → h.mu`** throughout (consistent with `reviveBySession`, `list→toInfo→getSession`).
  Never hold `m.mu` across `killSession`/`startManagedSession` (snapshot-then-unlock).
- `restartAgent` re-checks `tombstone[id]`/`wss[id]` under `m.mu` as the **last** step before acting.
- `archive` cancels + waits on `done` before removing the worktree (B1). `force` closed via `sync.Once`
  (double Kill-now clicks / archive race safe).

### 9.6 Handoff approval while attached (`setForceApprove`)
The desktop is almost always **attached** (rendering the terminal), and `maybeAutoYes` computes the
prompt only when `enabled && !attached` and `autoYesDecision` no-ops while attached
(`conpty_windows.go:355–392`). So normal AutoYes can't approve the handoff write.
- Add `setForceApprove(bool)` to `managedSession` (+ `conptySession`, + `fakeSession` stub). When set,
  `maybeAutoYes` evaluates the **narrow** `detectPrompt` (approval-only — won't touch selection menus)
  and taps Enter **bypassing both the `attached` and the AutoYes-`enabled` gates**, for the handoff
  window only.
- **Minimize exposure** (not a hard 1-cap — a legit handoff may need read-then-write): disengage
  force-approve the instant `handoffReady` fires.

### 9.7 Pre-send + seed readiness
- **Before** sending the handoff prompt: wait for the agent to be `!busy` (bounded ~10 s) so the prompt
  isn't interleaved into a mid-turn agent (Opus minor).
- **Seeding:** after the new session starts, poll `agentStatus()` until the agent reaches its initial
  idle/prompt (or a floor delay), bounded ~10 s, then `sendKeys(seedPrompt+"\r")` — CLIs flush stdin on
  boot, so seeding too early drops the message (Gemini M1, all R4). Timeouts injectable; correctness
  is **manual-QA-verified** (the fake can't reproduce boot timing).

### 9.8 Renderer (`desktop/src/`)
- **`CenterPane.tsx`** — `↻ Regenerate` button beside AutoYes; opens the modal; `disabled={regenerating}`.
- **`RegenerateModal.tsx`** (new) — confirm + handoff checkbox; renders phase progress + **Kill now**.
- **`App.tsx`** — `onRegenerate(id, handoff, cols, rows)` → `window.cs.regenerateAgent(...)`;
  `onKillNow(id)` → `window.cs.forceRegenerate(...)`; **suppress the alive→dead "Agent finished"
  notification while `w.regenerating`** (`App.tsx:74–81`); bump Hello to v5 (prefer importing
  `PROTO_VERSION` over the hardcoded literal, to stop drift). The terminal **auto-re-attaches** because
  `selected.sessionName` changes → `CenterPane`'s `key={workspace.sessionName}` remounts `TermView`.
- **`preload/index.ts`** — `regenerateAgent(id, handoff, size)` + `forceRegenerate(id)` wrappers.
- **`main/host-client.ts`** — `PROTO_VERSION 4→5`; extend `WorkspaceInfo` (`regenerating`,
  `regenPhase?`), `Request.method` union (+2 methods), `handoff?: boolean`.
- **`styles.css`** — `.regen-btn` consistent with `.autoyes`.

### 9.9 Deterministic transcript fallback
When `handoffReady` proceeds via `forced`/`inactivity`/`hardcap` **and** `!fileChanged` (the agent
produced no usable handoff), the daemon writes `HANDOFF.md` itself from `session.capture(full)`
(scrollback + screen, `conpty_windows.go:251–266`), under a header like *"Auto-captured transcript
(agent did not write a handoff)"*. No approval, no agent cooperation — the new agent **always** gets
some context (Opus alternative). This path is fully deterministic and **unit-testable**.

### 9.10 Prompts (hardcoded constants, MVP)
**Handoff** (note: "current working directory" — the agent's cwd is the worktree, where the detector
watches; optional sentinel line):
```
You are about to be replaced by a fresh agent in this same workspace. Before that, write a
handoff document named HANDOFF.md in the current working directory. Capture, concisely:
- Task: what you were asked to do and the goal.
- Current status: what is done, in progress, and what works / does not.
- Next steps: the concrete actions the next agent should take, in order.
- Key context: important files, decisions, constraints, gotchas, and build/test commands.
Write ONLY that file — make no other changes. When it is fully written, print: HANDOFF_COMPLETE
```
**Seed:**
```
A previous agent in this workspace left a handoff at HANDOFF.md in the current working directory.
Read it first, then continue the work it describes. If anything is unclear, inspect the relevant
files before proceeding.
```

## 10. Acceptance criteria

- **AC-1 (button):** With a workspace selected, a Regenerate control appears next to AutoYes; hidden
  with no selection; disabled while `regenerating`.
- **AC-2 (popup):** Clicking opens a popup with the handoff checkbox **checked by default**; Cancel
  changes nothing.
- **AC-3 (no-handoff restart):** Confirming with handoff **off** replaces the agent in-place; same
  branch/worktree; `alive` returns true again; no `HANDOFF.md`; persisted `AgentSessionID` (copilot)
  **differs** from before (verified via `workspaces.json`).
- **AC-4 (handoff happy path):** Confirming with handoff **on** sends the prompt; after `HANDOFF.md`
  is written and the agent idles, the session is replaced and the new agent receives the seed;
  `HANDOFF.md` exists in the worktree.
- **AC-5 (fresh context):** After any regenerate the copilot `AgentSessionID` differs (no resume);
  manual QA confirms the new agent shows no prior conversation context.
- **AC-6 (kill now):** During `handoff`, Kill now proceeds **promptly** (≪ hard cap); if no usable
  handoff exists, the transcript fallback populates `HANDOFF.md` before seeding.
- **AC-7 (timeout safety):** If the agent never writes `HANDOFF.md`, the inactivity timeout completes
  the regenerate, the transcript fallback runs, and the workspace ends **live or revivable**.
- **AC-8 (preservation):** Worktree changes, branch, run command, and AutoYes are unchanged; a running
  dev server (`runs.*`) keeps running.
- **AC-9 (no concurrent regen):** A second Regenerate on the same workspace while one is active is
  rejected/ignored; the button is disabled (covers the fast path too).
- **AC-10 (graceful degrade):** A non-copilot workspace still regenerates; id rotation is a no-op.
- **AC-11 (version gate):** A v5 app refuses a daemon < v5 with a clear banner.
- **AC-12 (re-attach):** After a regenerate completes, the agent terminal shows the **new** session's
  output and typed input reaches the new agent **without** reselecting (FR-11).
- **AC-13 (no false notification):** No "Agent finished" notification fires during a regenerate (FR-12).
- **AC-14 (no zombie):** After restart, exactly one agent session exists for the workspace; the old
  `SessionName` is gone (no lingering duplicate ConPTY).
- **AC-15 (archive mid-regen):** Archiving during a regenerate cancels it, leaves **no** zombie
  session/child, and fully removes the worktree.

## 11. Tests needed

> **Honesty about the fake (Opus, GPT-5.5 M4):** `startTestHost`'s `fakeSession` has
> `agentStatus()` always idle and a `sendKeys` that records bytes but approves nothing and writes no
> files. So force-approve, busy-gating, and seed timing are **not** meaningfully exercised by the fake
> as-is. Mitigations below.

### 11.1 Go unit (pure / fast)
- **`TestHandoffReadyDecision`** — table test of `handoffReady` across every branch: forced; sentinel;
  file-stable-idle (incl. `agentWaiting=true` still proceeds); inactivity; hardcap; and the
  keep-waiting cases (busy, not-yet-stable, within grace). **This is the primary safety net.**
- **`TestRestartAgentRotatesSessionAndID`** — inject a `workspace` directly into a `workspaceManager`
  backed by `startTestHost` fakes (bypassing `CreateWorkspace`'s `LookPath`, which blocks a `copilot`
  integration test). Assert: `SessionName` rotated, copilot `AgentSessionID` rotated to a valid v4
  UUID (non-copilot stays `""`), persisted before the old session is gone, new session alive, same
  worktree/branch.
- **`TestTranscriptFallbackWritesHandoff`** — deterministic: a fake whose `capture(full)` returns a
  known transcript; assert `HANDOFF.md` is written with that content + header.

### 11.2 Go integration (`startTestHost` fakes; extend the fake)
- **Extend `fakeSession`** with optional knobs: report `busy`/`waiting`, and a hook that writes a file
  to the worktree on a given `sendKeys` (to simulate the agent producing `HANDOFF.md`). This unlocks
  real orchestration coverage.
- **`TestRegenerateNoHandoffFastPath`** — `RegenerateAgent{handoff:false}`: session replaced (new
  name, alive), same branch/worktree, AutoYes preserved, `Regenerating` toggles true→false.
- **`TestRegenerateHandoffWritesAndSeeds`** — scripted fake writes `HANDOFF.md`; assert completion,
  seed bytes delivered to the **new** session, `regens[id]` cleared.
- **`TestForceRegenerateShortCircuits`** — start a handoff regen, never write the file, `ForceRegenerate`
  → proceeds within a second or two (≪ hard cap), transcript fallback ran, ends with a live session.
- **`TestArchiveDuringRegenerateNoZombie`** (AC-15) — archive mid-regen; assert the goroutine is
  cancelled, no session is started into the removed worktree, worktree fully removed, `regens`/`tombstone`
  cleaned up.
- **`TestRegenerateStartFailureRevivable`** (FR-10) — force the new start to fail; assert metadata is
  persisted with the rotated name so a subsequent `Attach`→`reviveBySession` recovers, and `regens[id]`
  is cleared (button not stuck).
- All tests `t.Setenv("HOME")` **and** `t.Setenv("USERPROFILE")` (config-corruption gotcha).
- Add Go `Client` helpers (`RegenerateAgent`, `ForceRegenerate`) in `client_windows.go`.

### 11.3 Proto
- **`proto_test.go`** — round-trip the new `Request`/`WorkspaceInfo` fields; assert `Version == 5`.

### 11.4 TypeScript / app
- No unit runner in `desktop/`. Gate on `npm run lint && npm run typecheck && npm run build` green
  (incl. the new component + types + version bumps).

### 11.5 Manual QA (Windows, real copilot) — covers the timing-sensitive, fake-untestable paths
1. Regenerate **without** handoff → instant fresh agent; same files; terminal re-attaches; input works.
2. Regenerate **with** handoff → `HANDOFF.md` appears; new agent reads it and continues.
3. **Kill now** mid-handoff → stops promptly; transcript fallback seeds the new agent.
4. **AutoYes off, attached** → the handoff write is still approved (force-approve, §9.6).
5. Uncommitted changes preserved; `HANDOFF.md` shows in the Changes tab.
6. New agent shows **no prior context** (fresh conversation).
7. No spurious "Agent finished" toast during regenerate.
8. Daemon **v4** + app **v5** → clear version-mismatch banner.

## 12. Open questions (genuinely unresolved)

- **OQ-1:** Default thresholds (`graceMs 4 s`, `inactivityMs 30 s`, `hardCapMs 5 min`) — tune during QA.
- **OQ-2:** Should `regenPhase` distinguish a `fallback` sub-state (transcript being written) in the UI,
  or is "restarting" enough?
- **OQ-3:** Start-before-kill (start the new named session, verify alive, then kill old) would upgrade
  FR-10 from *revivable* to *always-live*, at the cost of two ConPTY children briefly sharing the
  worktree. Deferred (post-handoff the old agent is idle, so the simpler kill-then-start + revivable is
  acceptable) — revisit if revival proves janky.

## 13. Build / verify (from `desktop/HANDOFF.md`)
```pwsh
# Go (Windows)
$go = "$env:TEMP\goroot125\go\bin\go.exe"; $env:GOTOOLCHAIN="local"; $env:GOFLAGS="-mod=mod"
& $go build ./...; & $go test ./...
& "$env:TEMP\goroot125\go\bin\gofmt.exe" -w <changed .go files>   # explicit paths
& $go build -o dist\cs.exe .; & $go build -o cs.exe .            # rebuild daemon; stop stale cs.exe

# Go (Linux verify — must also pass)
wsl -d Ubuntu bash -lc 'cd /mnt/d/dev/claude-squad; export GOTOOLCHAIN=local GOFLAGS=-mod=mod; /home/bendog/go125/go/bin/go build ./... && /home/bendog/go125/go/bin/go test ./...'

# App
cd desktop; npm run lint; npm run typecheck; npm run build
```
Windows-only Go goes in `*_windows.go`; add `!windows` stubs for any symbol referenced cross-platform.
After a Go change, rebuild `dist\cs.exe` and stop the stale `cs.exe` so the app uses the new daemon.
