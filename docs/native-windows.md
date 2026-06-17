# Native Windows Support — Architecture & Handoff

> **Audience:** maintainers and AI agents picking up the native-Windows work in this fork
> (`thirschel/Hangar`). This document explains **what** was built, **why** it was built that
> way, and **what alternatives were considered and rejected**. Read this before changing anything
> under `session/winhost/` or the Windows attach/AutoYes/persistence paths.

---

## TL;DR

Hangar upstream manages each agent in a **tmux** session (Unix only). This fork adds a
**native Windows backend** — no WSL, no tmux — that reaches tmux feature parity using a
**tmux-style client/host split**:

- A detached **session-host** process (`cs session-host`) owns one **ConPTY** console per agent and
  continuously renders each console through a **VT emulator** (`charmbracelet/x/vt`).
- The **TUI** (`cs`) is a thin **named-pipe RPC client**. Preview = "capture the emulator screen";
  attach = "stream the console + take over the real terminal"; AutoYes lives in the host.

Because the consoles live in the host (not the TUI), **sessions survive TUI restarts** (just like
tmux's server outliving a client). Everything is gated behind the existing
`session.TerminalSession` interface, so **Unix/tmux behavior is unchanged**.

Status: feature-complete and tested on Windows and Linux. Branch: `windows-native`.

---

## 1. Background — the original problem

The fork started from a user report on a **WSL-on-Windows** machine:

```
error capturing pane content: exit status 1
... wrote logs to /tmp/hangar.log   (but the file "didn't exist")
```

Root cause (diagnosed first, fixed in commit `b5c72f9`): the **agent program never actually ran**.
On the failing machine `copilot` died instantly with `GLIBC_2.28 not found` (the WSL distro was too
old; GitHub Copilot CLI needs glibc 2.28+ / Node 22+). When the agent dies instantly, its tmux
session disappears, so the *next* `tmux capture-pane` fails with a bare `exit status 1`, and the
real error was swallowed. The "missing" log existed in the **Linux** `/tmp`, not `C:\tmp` — the user
was looking from Windows. Commit `b5c72f9` surfaces the real stderr, detects dead sessions, and
guards the log message so this class of failure is legible.

That fix made WSL usable, but the user wanted to **avoid WSL entirely** and run `cs` natively on
Windows. That is what the rest of this work delivers.

---

## 2. Why the first Windows attempt (PR #248) didn't work

Upstream PR [#248](https://github.com/smtg-ai/claude-squad/pull/248) ("Add Windows Support") was
ported into this fork first (commits `9f0b6e3`, `c165491`, `2d852af`, merge `a46c1fa`). It ran
ConPTY **inside the TUI process** (`session/winterminal`). A root-cause analysis found three
**fundamental** flaws — not bugs, but architecture mismatches:

1. **No VT emulator.** `winterminal` stored **raw VT/ANSI bytes** in a line buffer and only split on
   `\n`. It never interpreted cursor moves, clears, SGR colors, or the alternate screen. Modern TUIs
   (claude, copilot) constantly repaint with cursor addressing, so the preview was blank/garbled/
   stale. ConPTY is **not** a multiplexer — it is a primitive pseudo-console; the parent process must
   implement screen buffering + VT parsing itself.
2. **No raw/VT console input on attach.** Attaching just copied bytes to `os.Stdout` without putting
   the console into raw + `ENABLE_VIRTUAL_TERMINAL_INPUT` mode, so interactive input hung.
3. **No persistence.** The HPCON (pseudo-console) handle lived in the TUI process, so it died with
   the TUI — the opposite of tmux's persistent server. There was nothing to reattach to.

Conclusion: the in-process approach could not be incrementally fixed into parity. It was retired
(see P4) in favor of the client/host split.

---

## 3. Alternatives we evaluated (and why we rejected them)

| Option | Native Win? | Persist/Detach | Capture equivalent | Embeddable in a Go `.exe`? | Verdict |
|---|---|---|---|---|---|
| **tmux via WSL2** | ❌ (Linux VM) | ✅ real tmux | ✅ `capture-pane` | ⚠️ via `wsl tmux …` | Rejected as the *primary* path: needs WSL; agents must be Linux-native; the user explicitly wanted to avoid WSL. Still works as today's `!windows` build. |
| **tmux via MSYS2/Cygwin** | ❌ POSIX layer | ✅ | ✅ | ❌ runtime dep, sessions invisible to native `.exe` | Rejected — isolated from the native Win32 console. |
| **ConPTY in-process** (PR #248) | ✅ | ❌ dies with TUI | ⚠️ raw bytes only | ✅ | Rejected — see §2. |
| **Zellij** (Rust) | ✅ (v0.41+) | ✅ server/client | ✅ via action pipe | ⚠️ external install | Rejected — hard external dependency to ship/manage. |
| **WezTerm mux** | ✅ | ✅ | ✅ `cli get-text` | ⚠️ requires WezTerm installed | Rejected — hard external dependency. |
| **Windows Terminal `wt.exe`** | ✅ | ❌ | ❌ no IPC/API | ❌ | Rejected — no programmatic multiplexer API. |
| **ConPTY + detached host** (chosen) | ✅ | ✅ (host owns HPCONs) | ✅ via VT emulator | ✅ `go get`, ships in the one binary | **Chosen** — see §4. |

The chosen option reproduces tmux's own design (a persistent server owning the PTYs + a thin client)
without any external runtime dependency, all inside the existing single `cs` binary.

---

## 4. The chosen architecture — tmux-style client/host split

```
  cs.exe (bubbletea TUI = thin client)        named pipes        cs session-host (detached, 1 per user)
  ── TerminalSession = RPC client    ── control:  \\.\pipe\… ──►  owns every ConPTY (survives TUI exit)
  ── preview  = CapturePane RPC      ── attach:   \\.\pipe\… ──►  per session:
  ── attach   = stream + raw console ◄───────────────────────►     xpty.Pty (ConPTY) + child process
  ── EnsureHost: connect or spawn-detached                          x/vt SafeEmulator (screen+scrollback)
                                                                     bounded rawRing (repaint fallback)
                                                                     ALWAYS-ON drain goroutine
                                                                     host-side AutoYes ticker
```

**Invariant that makes everything work:** the host **always drains** each ConPTY into its
`SafeEmulator` (+ a bounded raw ring + non-blocking fan-out to any attached client), regardless of
whether anyone is attached. So:

- **Preview** is just `SafeEmulator.Render()` (ANSI) or a plain cell-walk — the tmux `capture-pane`
  equivalent, always current.
- **TUI exit doesn't touch the consoles** → persistence.
- Always draining also avoids a `ClosePseudoConsole` deadlock that can occur pre-Win11 24H2 if output
  isn't being read.

### Data flows

- **Preview:** TUI → `CapturePane{screen|full}` RPC → host renders the emulator → returns text.
- **Attach:** TUI → `Attach` RPC → host returns a **per-session attach pipe name + one-time token** →
  TUI dials it, the host streams an emulator snapshot then live bytes, and the TUI takes over the real
  console (raw + VT). Ctrl-Q detaches; Ctrl-C passes through.
- **AutoYes:** the host's per-session ticker watches the emulator for an approval prompt and taps
  Enter itself (paused while attached). The TUI/daemon do **not** also tap (no split-brain).

---

## 5. Code map

All new Windows code lives under `session/winhost/` and is behind `//go:build windows` (except the
cross-platform shim files). Unix builds never compile it.

| File | Build tag | Responsibility |
|---|---|---|
| `proto/proto.go` | cross-platform | Wire protocol: length-prefixed JSON frames, method names, `Request`/`Response`, size limits, `ReadFull`. |
| `proto/proto_test.go` | cross-platform | Frame round-trip, oversize-header/payload rejection, truncated-body errors. |
| `winhost.go` | cross-platform | `SessionHostCmd` const, `ErrSessionGone`, `VersionMismatch` + `AsVersionMismatch` (so the TUI can detect skew without importing Windows code). |
| `runhost_other.go` | `!windows` | Stubs: `RunHost`, `HostInfo` (so `main.go` compiles everywhere). |
| `winhost_test.go` | cross-platform | `AsVersionMismatch` tests (run on Linux too). |
| `paths.go` | cross-platform | `~/.hangar/host.{json,lock,log}` paths + the `hostInfo` struct. |
| `host_windows.go` | windows | go-winio pipe server, session registry, request dispatch, idle-exit, `RunHost`, attach plumbing. |
| `conpty_windows.go` | windows | `conptySession`: ConPTY via `xpty`, `x/vt SafeEmulator`, always-drain goroutine, subscriber fan-out, rawRing, **host-side AutoYes** (`autoYesLoop`/`maybeAutoYes`/`autoYesDecision`/`detectPrompt`). |
| `client_windows.go` | windows | Control `Client` (one request/response per call), `EnsureHost`, `connectAndHello`, `transportError`. |
| `session_windows.go` | windows | `winhost.Session` implements `session.TerminalSession`; `Shutdown`, `HostInfo`, trust-prompt handling, `DetachSafely` (= kill, see §6). |
| `lock_windows.go` | windows | `host.lock` (`LockFileEx`) singleton + SDDL helpers. |
| `host_windows_test.go`, `session_windows_test.go`, `conpty_windows_test.go`, `conpty_autoyes_e2e_test.go` | windows | Lifecycle, attach token, ConPTY echo/resize, AutoYes decision + pause-while-attached, opt-in real-copilot e2e. |

Cross-cutting (touch shared code, stay Unix-safe):

- `session/terminal.go` — added `SetAutoYes(bool) error` to the `TerminalSession` interface.
- `session/terminal_windows.go` / `terminal_unix.go` — `NewTerminalSession` / `CleanupTerminalSessions`
  per platform (Windows → winhost + `Shutdown`).
- `session/instance.go` — `SetAutoYes` propagation; `ErrSessionGone` recreate-on-restore.
- `session/tmux/tmux.go` — no-op `SetAutoYes` (AutoYes stays TUI/daemon-driven on Unix).
- `app/app.go`, `app/help.go`, `app/attach_windows.go`, `app/attach_other.go` — the attach hand-off
  (see §6).
- `ui/terminal*.go` — the in-TUI Terminal tab is disabled on native Windows.
- `main.go` — `cs debug` prints host info.

---

## 6. Key design decisions & rationale

**One binary, hidden `session-host` subcommand.** The host is `cs session-host` (no TUI init in that
mode), spawned **detached** (`DETACHED_PROCESS | CREATE_NEW_PROCESS_GROUP`, nil std handles). Avoids
shipping a second executable.

**Singleton + lifetime.** The host holds `host.lock` (`LockFileEx`) for its lifetime and writes
`host.json` (pipe name, PID, **process creation time**, nonce, version — PIDs get reused, so PID
alone is insufficient). `EnsureHost()` tries the control pipe, else spawns and waits for `Hello`. The
host **idle-exits** after a grace period when there are 0 sessions AND 0 clients; `Shutdown`/`cs
reset` always stop it.

**Protocol.** Minimal length-prefixed JSON over go-winio **byte-mode** pipes: `io.ReadFull`, a max
frame size, request IDs, per-call deadlines. Per-user **SDDL** restricts the pipe to the current
user's SID. The **attach pipe is separate** and carries a **one-time auth token** — never trust the
session name alone.

**Emulator is authoritative.** Repaint/capture uses `SafeEmulator.Render()` (ANSI) as primary; the
bounded `rawRing` is only a supplementary fallback. The drain goroutine must **never block on a slow
subscriber** (per-subscriber bounded channel; drop on overflow) and **never hold a lock during pipe
I/O**.

**Attach hand-off via bubbletea `tea.Exec` (the subtle one).** *(commit `0f28d83`)* A plain blocking
attach left bubbletea's own input reader consuming `CONIN$` (stolen keystrokes) and did no full
repaint on detach (the TUI rendered on top of the agent's leftover screen). The fix routes the
Windows attach through `tea.Exec`, so the event loop **ReleaseTerminal** (stops its input reader +
renderer, restores console mode) → runs the attach, which sets its **own** raw/VT console mode and
streams → **RestoreTerminal** (re-enters alt-screen = full repaint) → delivers `attachFinishedMsg`.
The Unix/tmux path is unchanged and stays on the original blocking approach (it relies on bubbletea's
raw mode being active), gated by `app/attach_{windows,other}.go` build tags. Detach uses a **two-path
cancel**: `cancelreader.Cancel()`, and because that can return `false` under
`ENABLE_VIRTUAL_TERMINAL_INPUT`, the host also closes the attach pipe so the blocked read unblocks
with `ERROR_BROKEN_PIPE`.

**Host-side AutoYes.** *(commit `0199896`)* AutoYes lives in the host so prompts are auto-approved
**even while the TUI is closed** (the host is the Windows equivalent of upstream's autoyes daemon),
and it **pauses while a client is attached** so it never approves a prompt out from under the user.
Mechanics in `conpty_windows.go`:
- `detectPrompt(program, screen)` matches the agent's approval prompt. For **copilot** the match is
  `"No, and tell Copilot what to do differently"` — the reject option that appears on *every* copilot
  approval prompt (shell, edit, …), so it is prompt-type-agnostic (claude/aider/gemini unchanged).
- `autoYesLoop` ticks ~400 ms and calls `maybeAutoYes`, whose pure core `autoYesDecision(enabled,
  attached, prompt, armed)` taps Enter **once per prompt appearance** (edge-triggered: fire on the
  rising edge, re-arm when the prompt clears).
- `attachedCnt` (tracked in `subscribe`/`unsubscribe`) pauses AutoYes during an attach.
- **No double-tap:** the Windows `Session.TapEnter` is a **no-op** (the host owns auto-Enter), so the
  app's and daemon's `instance.TapEnter()` can't also fire. Trust-prompt dismissal uses `SendKeys`
  instead and now also covers copilot.
- Propagation: `TerminalSession.SetAutoYes` (no-op on tmux; RPC on Windows), called from
  `Instance.SetAutoYes` and on `Instance.Start`.

**Pause = kill, Resume = fresh.** *(commit `49d17e8`)* On Unix, pause detaches the tmux client and
keeps the session. On Windows a ConPTY is bound to its worktree directory, which Pause removes —
leaving a live process in a deleted directory is wrong. So Windows `Session.DetachSafely` **kills**
the host session; `Resume` then starts a **fresh** agent in the recreated worktree
(`DoesSessionExist()` is false). The prior agent conversation is not restored (documented tradeoff).

**Version skew is non-fatal.** `EnsureHost` returns `VersionMismatch` when an old host (from a
previous `cs` version) is still running. The TUI detects it via `AsVersionMismatch` and prints
actionable guidance (run `cs reset`) instead of crashing or silently killing live sessions.

**Terminal tab disabled on Windows.** The in-TUI Terminal tab imports `session/tmux` and launches
`/bin/sh`; porting it is out of scope. Use attach (Enter) instead.

---

## 7. Phase history (P0–P9)

| Phase | Commit | Summary |
|---|---|---|
| Diagnostics | `b5c72f9` | Surface real agent/tmux errors; detect dead sessions; guard log message. |
| PR #248 port | `a46c1fa` (+`9f0b6e3`/`c165491`/`2d852af`) | In-process ConPTY port (later retired). |
| P0 | (spikes, not committed to repo) | De-risked: Go 1.25 + deps build; `x/vt` renders screen; go-winio SDDL pipe + close-unblocks-read; console raw/VT mode dance. |
| P1 | `47c9844` | Session-host skeleton + named-pipe protocol (fake echo session). |
| P2/P3 | `1fc928d` | Real ConPTY sessions + `x/vt` emulator; capture + status over RPC. |
| P4 | `1d320b8` | Wire the host into the TUI; dead-vs-transport handling; retire `winterminal`. |
| P5 | `37b8ff8` + fix `0f28d83` | Interactive attach + the `tea.Exec` console hand-off. |
| P6 | `0199896` | Host-side AutoYes. |
| P7 | `49d17e8` | Persistence/lifecycle: Pause=kill/Resume=fresh, `cs debug` host info, graceful version skew. |
| P8 | `6638362` | Cross-platform `AsVersionMismatch` test + pause-while-attached integration test. |
| P9 | `85f35e2` | README rewrite for the session-host model. |
| Test hygiene | `5171152` | Make `config`/`tmux` tests hermetic on Windows (see §9). |

---

## 8. Build, run, and test

**Toolchain:** the ConPTY/VT deps require **Go 1.25+** (`go.mod`: `go 1.25.0`, `toolchain
go1.25.11`). Key pinned deps: `charmbracelet/x/xpty`, `.../x/conpty`, `.../x/vt`,
`charmbracelet/ultraviolet` (pseudo-version, no tag — keep it pinned in `go.sum`),
`Microsoft/go-winio`, `muesli/cancelreader`.

**Build:** `build.bat` (finds Go on PATH or in `C:\Program Files\Go`) → `cs.exe` in the repo root.

**Run:** `cs` from inside a git repo, with the agent (`copilot`, etc.) resolvable (`where copilot`).

**Tests:**
- Windows: `go build ./...` then `go test ./...` — should be fully green.
- Linux parity: build + `go test ./...` under WSL with a Go 1.25 toolchain. The `winhost` package is
  Windows-only, but the cross-platform files (`proto`, `winhost.go`, `winhost_test.go`) and all
  shared code compile and test on Linux. tmux behavior must stay unchanged.
- **Opt-in real-copilot e2e** (`conpty_autoyes_e2e_test.go`): proves the host approves a real copilot
  prompt on its own. Run with `COPILOT_AUTOYES_E2E=1` and `copilot` logged in on PATH.

**Quirks / gotchas when working on this:**
- Leftover `cs session-host` processes hold `host.lock`. Before cross-process e2e runs, kill stray
  `session-host` PIDs and remove `~/.hangar/host.json` + `host.lock`.
- `xpty.WaitProcess` is used instead of `cmd.Wait` (Go #62708 — `cmd.Wait` can deadlock with ConPTY).
- `SafeEmulator` has no `String()`; use the cell-walk helpers (`plainScreen`/`scrollbackPlain`).
- PowerShell: `gofmt -w <dir>` works but the `*.go` glob doesn't; `&&` only chains native commands.
- git `autocrlf` prints many LF→CRLF warnings on commit — cosmetic.
- `omitempty` on `bool` proto fields omits `false` from JSON (a PowerShell client sees `$null`).

---

## 9. Known limitations & gotchas

- **No cross-reboot persistence.** Sessions live only while the host process lives (same as tmux's
  server dying on reboot). After a reboot, `cs` recreates each missing session in its worktree.
- **Pause/Resume loses the agent conversation** on Windows (fresh start by design).
- **Terminal tab disabled** on native Windows.
- **AutoYes prompt strings are agent-version-specific.** The copilot match was captured from copilot
  **1.0.63**; if GitHub changes the prompt wording, update `detectPrompt`. The strings live in exactly
  one place (`conpty_windows.go`).
- **Two pre-existing Windows-only test failures were test bugs, now fixed** (commit `5171152`):
  config tests set `HOME` but Windows `os.UserHomeDir` reads `USERPROFILE`; and `TestStartTmuxSession`
  left a mock PTY handle open so `t.TempDir` cleanup couldn't delete it on Windows. Watch for the same
  two patterns in new tests (set both env vars; close handles via `t.Cleanup`).

---

## 10. Agent prompt strings (for AutoYes / trust handling)

Captured from **GitHub Copilot CLI 1.0.63** (your mileage may vary across versions):

- **Tool/command approval** (host AutoYes matches the option-3 line):
  ```
  Do you want to run this command?
   ❯ 1. Yes
     2. Yes, and don't ask again for `<cmd>` in this directory (…)
     3. No, and tell Copilot what to do differently (Esc to stop)
  ```
- **Folder trust** (same string as claude; Enter selects "Yes"):
  ```
  Do you trust the files in this folder?
   ❯ 1. Yes
     2. Yes, and remember this folder for future sessions
     3. No (Esc)
  ```

To re-capture for a new agent/version, drive the agent through a ConPTY (`xpty.NewPty` →
`cmd.exe /c <agent>`), feed output into a `vt.Emulator`, and dump `emu.String()` while triggering a
non-auto-approved command (e.g. a delete). A throwaway harness for this lived in `D:\dev\cs-spikes\`.

---

## 11. For the next agent

- **Adding a new agent (besides copilot/claude/aider/gemini):** add its approval-prompt match to
  `detectPrompt` in `conpty_windows.go` and (if it has a startup trust prompt) to
  `CheckAndHandleTrustPrompt` in `session_windows.go`. Capture the real strings — don't guess.
- **Changing the protocol:** bump `proto.Version`. The version-skew path (`EnsureHost` →
  `VersionMismatch` → `AsVersionMismatch` → TUI guidance) already handles an old host gracefully.
- **Anything touching the drain loop / attach:** preserve the invariants in §6 (never block the
  drain on a subscriber; never hold a lock during pipe I/O; keep the emulator authoritative).
- **Always verify on both platforms:** `go test ./...` on Windows *and* under WSL (Go 1.25). Keep the
  Unix/tmux path untouched.
- **Possible future work:** cross-reboot persistence (re-spawn from `state.json` + `copilot
  --resume`); port the Terminal tab to the host model; richer `cs debug`; ANSI scrollback in
  `CapturePane(full)`.

---

## 12. Quick reference

```text
cs                      # launch the TUI (spawns/uses the session host)
cs debug                # config + log paths + session-host pipe/PID/version/sessions
cs reset                # clear instances, remove worktrees, stop the daemon AND the session host
cs session-host         # (hidden) the detached host process; you won't run this by hand
```

State in `~/.hangar/`: `state.json` (instances), `config.json`, `daemon.pid`, and on Windows
`host.json` + `host.lock` (the session host).

Key types: `winhost.Session` (implements `session.TerminalSession`) · `conptySession` (one ConPTY +
emulator) · `host` (registry + dispatch) · `Client` (control RPC) · `proto.Request`/`Response`.
