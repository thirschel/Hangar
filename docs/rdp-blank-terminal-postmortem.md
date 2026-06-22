# Postmortem — Copilot agent pane hangs blank (VT emulator reply-pipe deadlock)

> **Status:** Fixed · **Platform:** Windows (native session host) · **Severity:** High (agent unusable)
> **Root cause confirmed on-box, 2026-06-20.** This document supersedes the earlier "launch
> environment" hypothesis from the exploratory investigation notes (that hypothesis was
> **disproven**; see §"Why it escaped").

## Summary

On Windows, every AI-agent pane (copilot, and any agent that probes terminal capabilities at startup)
rendered blank and never recovered. The cause was **not** RDP, the GPU, the environment, the launch
path, or copilot itself: the session host's output-drain goroutine **deadlocked inside the
`charmbracelet/x/vt` terminal emulator**. The emulator answers terminal queries by writing the reply to
an **unbuffered `io.Pipe`**, which blocks until the pipe is read — and the drain never read it.
copilot's first startup query (`ESC [ ? 2026 $ p`, a DECRQM probe for synchronized-output support)
wedged the drain permanently, which back-pressured the agent to a halt and froze the whole session.

The fix adds a goroutine (`pumpEmuReplies`) that continuously drains the emulator's reply pipe **and
feeds the replies back to the agent**, as a real terminal would. A real copilot session that previously
froze at a handful of bytes / blank now renders fully.

> **The rendering fix is a separate, real fix and remains correct.** The 2D-canvas terminal renderer
> (`canvasRenderer.ts`) addresses the genuine RDP/no-GPU *presentation* problem. This deadlock is an
> unrelated, host-side bug that produced an identical-looking blank pane — which is exactly why the two
> were conflated for so long.

## Error manifestation

What the user saw:

- The agent pane is blank. The status spinner never appears; the copilot UI never draws.
- The bottom shell pane (pwsh/cmd) renders fine — which strongly (and misleadingly) suggested an
  agent-launch or environment problem rather than a rendering/host problem.
- Because the box was a GPU-less RDP session, the blank pane looked like the (separate, already-shipped)
  RDP/Chromium presentation problem. It was not.

Host-log fingerprint (`~/.hangar/host.log`):

```
conpty start argv ... path="copilot" args=[...]
conpty first byte ... bytes=179 totalBytes=179
conpty drain progress ... intervalBytes=7 totalBytes=186      <- then NOTHING, ever
```

The agent always froze after a tiny, variable number of bytes (observed: 7, 179, 186) and then emitted
nothing more. When a client tried to read the screen, the host's control plane timed out
(`CapturePane i/o timeout`) — a key clue that a **lock was held**, not that copilot had simply stopped
writing.

**Scope:** all query-emitting agents on the native Windows session host. Shell sessions were unaffected
(they don't send capability queries). Unix/macOS (tmux backend) was unaffected — it does not use the vt
emulator.

## Root cause

The native Windows session host renders each agent's output through an in-process VT emulator
(`charmbracelet/x/vt`'s `SafeEmulator`) so the TUI/desktop client can paint the screen and AutoYes can
read it. The drain goroutine pumps agent bytes into the emulator:

```go
// session/winhost/conpty_windows.go — drain()
s.subMu.Lock()
_, _ = s.emu.Write(data)   // <-- feeds agent output to the emulator
...
s.subMu.Unlock()
```

The vt emulator is **also a terminal**: when the agent sends a query (DECRQM `ESC[?…$p`, device
attributes `ESC[c` / `ESC[>c`, cursor-position `ESC[6n`, color reports, …), the emulator generates the
reply and writes it to an internal pipe so the host can route it back to the agent:

```go
// charmbracelet/x/vt/emulator.go
t.pr, t.pw = io.Pipe()                                          // the reply channel
// charmbracelet/x/vt/csi.go — handleRequestMode (DECRQM)
_, _ = io.WriteString(e.pw, ansi.ReportMode(mode, setting))     // writes the reply
```

`io.Pipe` is **synchronous and unbuffered** — a write to `pw` blocks until something reads `pr`
(`emu.Read`). The session host never read the emulator's reply pipe. So the first query an agent emits
blocks `emu.Write`, and because the drain calls `emu.Write` while holding `subMu` **and** the emulator's
own lock (`SafeEmulator.mu`, taken by `Write` but **not** by `Read`), the entire session seizes:

- `emu.Write` never returns → the drain goroutine is stuck forever.
- The drain stops reading the ConPTY → its output pipe fills → copilot back-pressures and stops doing
  anything (hence the blank, "frozen at N bytes" pane).
- The stuck drain holds `subMu` and the emulator lock → attach/subscribe/`CapturePane` block (the
  observed `i/o timeout`).

The violated design assumption: *"feeding agent output into the emulator with `emu.Write` is a
non-blocking, fire-and-forget render step."* It is not — **the emulator can talk back, and its reply
channel must be drained.**

copilot trips this immediately: among the very first bytes it emits at startup is `ESC[?2026$p` (DECRQM
for mode 2026, synchronized output). pwsh/cmd never send such queries, which is exactly why the shell
pane rendered and the agent pane did not.

## Why it escaped

- **Wrong-layer symptom.** The visible failure (blank pane on a GPU-less RDP box) pointed straight at the
  renderer, where a genuine, separate RDP problem had recently been fixed (the 2D-canvas terminal
  renderer). The two issues were conflated; the agent hang was attributed to the **launch environment**.
  That hypothesis was wrong.
- **The shell pane worked.** A working shell next to a broken agent reads as "agent launch / environment
  problem," not "host render-path problem." In reality the discriminator was simply *which control
  sequences the program emits.*
- **Faithful external reproduction "passed."** copilot launched under the exact same backend
  (`charmbracelet/x/xpty`), in a console-less detached process, with the exact session-host environment
  and the real worktree — and rendered perfectly, **because a standalone harness has no emulator.** Only
  the host's emulator-in-the-drain reproduced the hang.
- **No regression test exercised a query-emitting program through the emulator.** Existing unit tests
  feed plain text / escape sequences that don't trigger an emulator reply, so the blocking path was never
  hit.

## Fix

`session/winhost/conpty_windows.go` — add a goroutine that continuously drains the emulator's reply pipe
and writes the replies back to the agent (so the host behaves like a real terminal, answering the
agent's queries), started alongside `drain()`:

```go
func (s *conptySession) start() error {
    ...
    go s.drain()
    go s.pumpEmuReplies()   // <-- NEW: keep the emulator's reply pipe drained
    go s.wait()
    go s.autoYesLoop()
    ...
}

// pumpEmuReplies forwards the emulator's terminal replies back to the agent.
// The vt emulator answers queries (DECRQM, device attributes, cursor position,
// color reports, …) by writing to an UNBUFFERED io.Pipe; that write blocks until
// the pipe is read. drain() feeds agent output to s.emu.Write while holding subMu,
// so if nothing reads the reply pipe a single agent query wedges drain() forever
// (it stalls inside emu.Write holding subMu and the emulator lock, freezing the
// session). copilot triggers this at startup with its mode-2026 DECRQM probe.
func (s *conptySession) pumpEmuReplies() {
    defer recoverGoroutine("conpty.pumpEmuReplies")
    buf := make([]byte, 4096)
    for {
        n, err := s.emu.Read(buf)
        if n > 0 {
            s.writeMu.Lock()
            if pty := s.pty; pty != nil {
                _, _ = pty.Write(buf[:n])   // feed the reply back to the agent
            }
            s.writeMu.Unlock()
        }
        if err != nil {
            return
        }
    }
}
```

`SafeEmulator.Read` does **not** take the emulator lock (it calls `Emulator.Read` directly), so the pump
runs concurrently with `drain()`'s `emu.Write` — that is the whole point: the reader is always available
to unblock a reply write.

`close()` now waits for `drain` to finish, then closes the emulator so the pump exits cleanly (and,
defensively, so a wedged `emu.Write` is unblocked):

```go
if s.pty != nil {
    err = s.pty.Close()
}
// Closing the ConPTY makes drain()'s s.pty.Read return; wait (bounded) for drain
// to stop writing to the emulator, then close the emulator so pumpEmuReplies'
// emu.Read returns io.EOF and the goroutine exits instead of leaking.
if s.drainDone != nil {
    select {
    case <-s.drainDone:
    case <-time.After(procExitWaitTimeout):
    }
}
if s.emu != nil {
    _ = s.emu.Close()   // promoted Emulator.Close: pw.CloseWithError(io.EOF) -> Read returns EOF
}
```

`SafeEmulator` embeds `*Emulator`, so `s.emu.Close()` resolves to the promoted `Emulator.Close()`
(`pw.CloseWithError(io.EOF)`), which makes any pending `emu.Read` return `io.EOF`. Ordering the close
**after** `drainDone` keeps the shutdown free of an `emu.Write`/`emu.Close` race in the normal path; the
timeout is a defensive backstop that still releases a (hypothetically) wedged drain.

**Regression test:** `session/winhost/conpty_emu_reply_windows_test.go`
(`TestPumpEmuRepliesUnblocksDECRQM`, `TestPumpEmuRepliesForwardsReplies`) — drives the **real**
`pumpEmuReplies` method against a `SafeEmulator` and a capture pty, asserts that
`emu.Write("\x1b[?2026$p")` returns promptly while the pump drains, and that the Primary-DA reply is
forwarded back to the child. The first test **times out if the pump is removed** — exactly the
production deadlock.

## Validation (Windows host)

| Check | Before fix | After fix |
| --- | --- | --- |
| `emu.Write("\x1b[?2026$p")` with no reader | hangs (>2 s) | returns instantly (pump reads) |
| Live host launches real copilot | wedged at ~186 bytes, control plane `i/o timeout` | full UI renders |
| `go build ./...` | green | green |
| `go test ./session/winhost/` | green | green (+2 new tests) |
| `gofmt -l` (LF-normalized) | clean | clean |

> CI runs on `ubuntu-latest`, so the Windows-only (`//go:build windows`) host code and these tests are
> **not** compiled or run in CI — they must be validated on a Windows host.

## How it was found (the investigation)

A systematic elimination, because the symptom pointed at the wrong layer.

1. **Verified the symptom, not the story.** `host.log` showed the agent genuinely stalling at ~186 bytes;
   the shell pane drained normally. Confirmed the canvas renderer already worked (shell pane painted), so
   this was a *separate* problem from the RDP rendering fix.
2. **Built a faithful external harness** (`xpty`, the exact backend the session host uses) that launches
   copilot and writes raw bytes to a file. copilot rendered fully under the harness — because it has no
   emulator.
3. **Ruled out every launch variable, one at a time** (all still rendered): full / stripped / the exact
   132-var session-host environment (incl. `WT_SESSION`); console-less detached parent with NUL stdio;
   the real worktree cwd; terminal size; resize-during-startup; program resolution; `--resume`.
4. **Reproduced it in the live host.** Driving the running session host to launch copilot reproduced the
   blank (wedged at a few bytes) and every follow-up `CapturePane` returned `i/o timeout`. That isolated
   the difference to something the host does that the harness doesn't: **run agent output through vt's
   emulator.**
5. **Found the deadlock byte-exactly.** copilot's first emitted sequence is a small capability block; a
   focused test feeding candidate sequences to `vt.NewSafeEmulator` under a watchdog pinpointed it:
   `emu.Write("\x1b[?2026$p")` never returns. Reading the emulator source confirmed the unbuffered
   `io.Pipe` reply mechanism and that the host never drained it.

## Timeline

| Date (2026) | Event |
| --- | --- |
| Jun 15 | `c904ebe` "P2 real ConPTY sessions with x/vt emulator" wires the vt emulator into the session-host drain — **without** reading its reply pipe. *Bug introduced.* |
| Jun 15 → | Shipped in subsequent Windows releases (v1.6.x → v1.7.1); agent panes blank for query-emitting agents on Windows. |
| Jun 20 | On-box investigation; root cause isolated to the emulator reply-pipe deadlock; fix implemented and validated. |

## Prevention

| Action | Type | Status |
| --- | --- | --- |
| `pumpEmuReplies()` drains the emulator reply pipe (the fix) | Code | ✅ |
| `conpty_emu_reply_windows_test.go` — fails if the reply pipe isn't drained | Test | ✅ |
| **Codebase rule:** any code that writes to a vt emulator (`emu.Write`) must also drain its `Read`/reply pipe — the emulator can block on query replies | Docs/Memory | ✅ |
| Fold the concise root cause into `docs/native-windows.md` and retire the exploratory RDP blank-terminal notes | Docs | ✅ |
| Add an end-to-end winhost test that launches a real query-emitting program through a `conptySession` and asserts the screen renders | Test | ☐ |
| When bumping `charmbracelet/x/vt`, re-verify the reply-pipe contract (a buffered change could mask, or a stricter change could re-expose, this class of hang) | Process | ☐ |

## Notes & related

- The earlier canvas terminal renderer work (RDP/GPU-less presentation) was a **separate, real fix** and
  remains correct; it is unrelated to this deadlock.
- Cross-platform seam preserved: the Unix/macOS tmux backend does not use the vt emulator and was never
  affected.
- The benign data race on vt's `Emulator.closed` bool (read by `Read`, set by `Close` without a shared
  lock) is harmless here: `io.Pipe` is internally synchronized, so the pump's `Read` returns `io.EOF`
  once `Close` fires regardless of how the `closed` fast-path races. CI does not run `-race`.
