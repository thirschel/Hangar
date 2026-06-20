# Hangar blank-terminal — on-box debugging handoff

> **⚠️ RESOLVED — root cause found (2026-06-20). The central hypothesis in this handoff (the copilot
> hang is caused by Hangar's *launch environment*) was DISPROVEN.** The real cause was a deadlock in the
> session host's VT emulator: it answers terminal queries by writing to an **unbuffered `io.Pipe`** that
> nothing drained, so copilot's startup `ESC[?2026$p` DECRQM probe wedged the output drain and froze the
> session. **Fix shipped:** `pumpEmuReplies()` in `session/winhost/conpty_windows.go`. See
> **`docs/rdp-blank-terminal-postmortem.md`** for the authoritative write-up. The on-box tests below are
> retained only as a record of the (now-closed) investigation.

> **Purpose.** A self-contained handoff so you can debug the *remaining* problem **directly on the
> affected RDP machine**. It records the full problem set, everything tried, what's fixed, what's still
> open, the leading hypotheses, and the exact commands to run on the box. Companion to
> `docs/rdp-blank-terminal.md` (the design/findings doc).
>
> **Machine:** Windows 11 Enterprise 23H2 (22631.x), **RDP session**, no hardware GPU (Microsoft Remote
> Display Adapter / Hyper-V Video; MPO = Not Supported, Hardware Scheduling Off), Microsoft Defender for
> Endpoint (EDR) active, PowerShell `$PROFILE` under OneDrive (oh-my-posh).
> **App:** Hangar desktop 1.7.0 (Electron 42 / Chromium 148 / React 19 / xterm.js 6).

---

## 0. TL;DR — where we are

There were **two separate problems** behind "blank terminal":

1. **Rendering (FIXED).** On RDP/no-GPU, Chromium never *presents* xterm's DOM renderer, so panes are
   blank. **Fix shipped:** a single-surface **2D-canvas terminal renderer** (`canvasRenderer.ts`),
   auto-selected on software compositing. **Proven on the box:** the pwsh **shell pane renders** through
   it (`glyphsDrawn:461`, `capturePixelProbe sampledNonBackgroundPixels:10045`).

2. **The copilot agent pane is still blank (OPEN).** This is **not** a rendering bug. The diagnostic
   shows copilot emits ~186 bytes — it **clears the screen and then hangs without drawing its UI**
   (`glyphsDrawn:0`). The canvas renderer faithfully draws nothing because the buffer is empty.

**Latest decisive finding:** launching copilot **without** `--session-id` (plain `copilot`) **still
hangs** — so the session-seed was *not* the cause. Since plain `copilot` **works in a raw terminal on
this same box** (your earlier ConPTY analysis), the difference is **Hangar's launch environment**, not
copilot or the network.

**Most likely remaining cause:** Hangar launches the agent with the **Electron app's environment**
(system/Explorer env), which **lacks what your PowerShell login profile provides** (proxy vars, PATH
additions, tokens, Node settings). copilot needs one of those to finish startup; without it, it clears
the screen and waits forever. **See §4 for the exact tests to confirm this on the box.**

---

## 1. The two problems, precisely

### Problem 1 — blank panes on RDP/no-GPU (FIXED)
Chromium software-compositing does not present xterm's DOM-renderer layer (hundreds of positioned
`<span>`s) to the RDP screen. The React UI paints; the terminal layer doesn't. A 2D `<canvas>` *does*
present (verified). Fix = canvas renderer, default `auto` (canvas under software compositing).

### Problem 2 — copilot agent hangs at startup (OPEN)
copilot's complete output before it stops (decoded from `TermView data preview`):
```
\e[?25l   hide cursor
\e[2J     clear screen
\e[m      reset attributes
\e[H      cursor home
\r\n × ~40 blank lines
\e[H      home
\e]0;C:\Users\thirschel\AppData\Local\Microsoft\WinGet\Links\copilot.exe\x07   set window title (OSC 0)
\e[?25h   show cursor
\e[>4;2m  set modifyOtherKeys (XTMODKEYS)
```
…then **silence** (179 + 7 = 186 bytes total). **No query sequence** (`\e[c`, `\e[6n`, `\e[?u`) — so it
is **not** waiting on a terminal response. It is blocked **internally** (network/auth/subprocess/init).
The pwsh shell, by contrast, does the same clear-dance then keeps sending its prompt → renders fine.

---

## 2. On-box result timeline (what each build proved)

| # | Build / change | Result on the box |
| --- | --- | --- |
| 1 | Phase 0/1: occlusion flag + nudges + `capturePage` probe | Still blank. Probe showed a *populated, live* composited surface (whole-window) → present-path failure (H1). |
| 2 | `--disable-direct-composition` (remote-gated) + **terminal-rect** probe | Still blank. Terminal-rect probe = 32 px (blank) even with every present flag on. |
| 3 | Opt-in `--disable-gpu-compositing`/`--disable-gpu` | Still blank. **No Chromium flag** changes it. Key insight: React UI presents, only the terminal layer doesn't → xterm DOM renderer specific. |
| 4 | **Canvas-viability self-test** (animated `<canvas>` overlay) | **Canvas animates on screen** (`RenderSelfTest framesPerSec:~32`) while xterm blank → a single 2D canvas *presents*. Green-light the canvas renderer. |
| 5 | **Canvas renderer** + paint diagnostic | **SHELL renders** (`glyphsDrawn:461`, probe 10045). **Agent stays blank** (`glyphsDrawn:0`, `isAlt:false`) → copilot draws nothing. Rendering is fixed; agent issue is separate. |
| 6 | `TermView data preview` (raw agent bytes) | copilot clears screen + hangs (186 bytes, no TUI). Hypothesis: `--session-id` new-session handshake. |
| 7 | **"Resume agent sessions" opt-out** (launch plain `copilot`) | **Plain `copilot` (argCount=0) STILL hangs** (186 bytes). → `--session-id` was NOT the cause. Difference is the **launch environment**. |

---

## 3. What is fixed and how it works (so you don't re-debug it)

- **Canvas renderer:** `desktop/src/renderer/src/components/canvasRenderer.ts` — keeps xterm for
  parse/buffer/input/selection/scrollback; paints visible cells to one opaque `<canvas>` overlaid on the
  pane. Handles fg/bg (default/256-palette/truecolor), bold/italic/dim/underline/inverse, block cursor,
  selection, scrollback, DPR.
- **Selection:** Settings → Diagnostics → **Terminal renderer** = `Auto` (default) / `DOM` / `Canvas`.
  `Auto` → canvas when software compositing is detected, else DOM. Forced `Canvas` always uses it.
- **Repaint:** the canvas repaints on xterm `onRender`/`onScroll`/`onResize`/`onCursorMove`/
  `onSelectionChange`, **and** after every `term.write()` (guaranteed), plus catch-up paints at
  150/500/1200/2500 ms.
- The earlier RDP launch flags (occlusion, direct-composition, opt-in disable-GPU) and the
  fontSize/native nudges remain available but are **not** what fixes it; the canvas renderer is.

---

## 4. ⭐ Debug the copilot hang ON THE BOX — do these in order

The goal: prove whether copilot hangs because of the **environment** Hangar launches it with. All of
these run on the affected machine.

### Test A — run copilot inside Hangar's own pwsh shell pane (fastest, most decisive)
1. Open a Hangar workspace. In the **bottom shell pane** (pwsh — it loads your `$PROFILE`), type:
   ```
   copilot
   ```
2. **If copilot draws its UI here but not in the agent pane** → the agent pane's environment is missing
   what your profile provides. That's the cause. (The shell pane is `pwsh -NoLogo` *with* profile; the
   agent is launched **directly**, no profile.)
3. If copilot *also* hangs here → the difference is narrower (cwd/stdin/conpty), go to Test C.

### Test B — capture the exact environment the agent gets, and diff it
The agent runs with the **session-host's** environment (= the Electron app's `process.env`). Capture it
and compare to a normal login shell:
1. Temporarily set Hangar's agent program to a dumper. In `~/.hangar/config.json` set
   `"default_program": "cmd.exe /c set > %USERPROFILE%\\hangar-agent-env.txt"` (or use a workspace whose
   agent is that), create a workspace, then read `hangar-agent-env.txt` — **this is the agent's real
   environment.**
2. In a **normal PowerShell** (Start menu, profile loaded): `cmd /c set > $env:USERPROFILE\shell-env.txt`.
3. Diff them:
   ```powershell
   Compare-Object (Get-Content $env:USERPROFILE\hangar-agent-env.txt) (Get-Content $env:USERPROFILE\shell-env.txt)
   ```
4. **Look for vars present in the shell but missing in the agent:** `HTTP_PROXY` / `HTTPS_PROXY` /
   `NO_PROXY`, `GH_TOKEN` / `GITHUB_TOKEN` / `COPILOT_*`, `NODE_OPTIONS` / `NODE_EXTRA_CA_CERTS`,
   custom `PATH` entries, `APPDATA`/`LOCALAPPDATA`/`USERPROFILE`/`HOME`. Any of these could be what
   copilot needs. (Also look for Electron-injected vars present only in the agent, e.g.
   `ELECTRON_RUN_AS_NODE`, that might *break* a Node CLI.)

### Test C — launch the agent through a profile-loaded shell
Set the workspace/agent **shell** to PowerShell so copilot launches as `pwsh -NoLogo -Command copilot`
(loads your profile → profile env). In `~/.hangar/config.json` set `"default_shell": "pwsh"`, create a
new workspace.
- **If copilot now draws** → confirms it needs the profile environment; the fix is to launch the agent
  through a profile-loaded shell (or inject the missing vars). This is your earlier raw-test Case 2
  (`pwsh -NoLogo -Command copilot` PASSED).

### Test D — rule out network/auth/proxy directly
In a normal terminal on the box: `copilot --session-id=<any-new-uuid>` (a *new* session, like Hangar
seeds). If **that** hangs in a raw terminal too, the new-session path needs the network/proxy that your
profile sets — reinforcing Tests B/C. If it works, the issue is purely the Hangar-launch environment.

### Test E — confirm copilot is actually blocked (not just not-drawing)
While the agent pane is blank, check the copilot process: `Get-Process copilot` (note CPU/threads), and
if you have Process Explorer/Procmon, look at what the `copilot.exe` (and any child `node`) is waiting on
(a network connect, a named pipe, a file). A pending TCP connect to a GitHub endpoint = network/proxy; a
blocked file/stdin read = local.

---

## 5. Hypotheses, ranked (with the test that settles each)

| Likelihood | Hypothesis | Settled by |
| --- | --- | --- |
| **High** | Agent env (Electron/Explorer) lacks profile-set vars copilot needs (proxy / tokens / PATH / NODE_*). | Tests A, B, C |
| Medium | copilot's startup makes a network/auth call that needs the proxy vars only the profile sets. | Tests B, D, E |
| Low | An Electron-injected var (e.g. `ELECTRON_RUN_AS_NODE`) breaks the Node-based copilot. | Test B (diff), unset it in the host env |
| Low | cwd (fresh worktree) or stdin wiring differs from a raw terminal. | Test A vs C |
| **Ruled out** | `--session-id` seed / resume. | On-box result #7 (plain copilot hangs) |
| **Ruled out** | Rendering / canvas / xterm. | Shell renders via canvas (#5) |
| **Ruled out** | ConPTY itself. | Your ConPTY analysis: copilot works in raw ConPTY |

---

## 6. If a hypothesis confirms — the Hangar-side fix directions

- **Missing profile env (most likely):** launch the agent through a profile-loaded shell (set
  `default_shell: pwsh` → `agentcmd` already wraps it as `pwsh -NoLogo -Command <program>` *with* profile
  via `BuildLaunch`/`ShellPwsh`), **or** have the session-host inject the needed vars (proxy/tokens) into
  the agent's `cmd.Env`. Touchpoint: `session/winhost/conpty_windows.go:213` (ShellNone leaves `cmd.Env`
  nil → inherits host env; for pwsh/powershell it's `os.Environ()+spec.Env`).
- **Electron var leak:** strip `ELECTRON_RUN_AS_NODE` (and similar) from the env passed to the
  session-host. Touchpoint: `desktop/src/main/host-client.ts:602` (`env: { ...process.env, ... }`).
- **Proxy specifically:** ensure `HTTPS_PROXY`/`HTTP_PROXY`/`NO_PROXY` reach the agent.

These are **proposals to validate on the box** — none are implemented yet, because every Chromium/flag
lever and the session-seed have already been ruled out, and the env hypothesis hasn't been confirmed.

---

## 7. Failed / ruled-out approaches (do NOT repeat)

- `app.disableHardwareAcceleration()` — no-op (no GPU).
- `webContents.invalidate()` repaint — ineffective (removed).
- `--disable-features=CalculateNativeWinOcclusion` (+ backgrounding flags) — didn't fix the blank.
- `--disable-direct-composition` — didn't fix the blank.
- `--disable-gpu-compositing --disable-gpu` — didn't fix the blank.
- fontSize / cols / native-window repaint nudges — didn't fix the agent (no content to repaint).
- WebGL / SwiftShader / `@xterm/addon-canvas` — N/A on xterm 6 / no GPU (see `docs/rdp-blank-terminal.md`).
- **Launching plain `copilot` without `--session-id`** — still hangs (the latest result). So resume/seed
  is not the cause.

---

## 8. Settings & toggles reference (Settings → Diagnostics)

| Setting | Default | Effect |
| --- | --- | --- |
| **Terminal renderer** | `Auto` | `Auto`=canvas under software compositing; `DOM`; `Canvas` (force). |
| Disable window occlusion | on | `--disable-features=CalculateNativeWinOcclusion` + backgrounding flags (pre-ready). |
| Disable DirectComposition (remote) | on | `--disable-direct-composition` when `isRemoteSession()`. |
| Force-disable GPU compositing (last resort) | off | `--disable-gpu-compositing --disable-gpu`. |
| Terminal repaint nudge | `native` | `native`/`fontsize`/`cols`/`off` (no-op unless software compositing). |
| **Resume agent sessions** | on | off → launch agent **without** `--session-id` seed (plain copilot). |
| Terminal render diagnostics | off | enables `capturePixelProbe`, `canvas paint`, `TermView data preview` logging. |
| Terminal render self-test | off | animated `<canvas>` overlay to test canvas presentation. |

Config: desktop settings live in `~/.hangar/desktop.json`; daemon/agent settings in `~/.hangar/config.json`
(`disable_agent_resume`, `default_program`, `default_shell`, …). Logs: `~/.hangar/desktop.log` and
`~/.hangar/host.log` (Settings → Diagnostics → Open …). Set `HANGAR_DEBUG=1` (or Verbose logging) for
verbose host logs.

---

## 9. Diagnostic log markers (enable "Terminal render diagnostics" first)

- `gpu status {…}` — compositing/flags/remote state at startup.
- `capturePixelProbe {region:"terminal", sampledNonBackgroundPixels, checksum}` — pixels in the terminal
  region of the composited surface. ~32 = blank; thousands = content present.
- `canvas paint {glyphsDrawn, canvasW/H, cols, rows, viewportY, baseY, isAlt}` — what the canvas drew.
  **`glyphsDrawn:0` = the buffer is empty (agent drew nothing); >0 = renderer is drawing.**
- `TermView data preview {bytes, preview:"\e[…"}` — escaped preview of the first agent output chunks
  (this is how the 186-byte clear-and-hang was found).
- `RenderSelfTest raf {framesPerSec}` — canvas-overlay animation FPS.
- host log: `conpty start argv …` shows the exact agent command (`args:[…]`); `agent resume disabled …`
  shows the no-seed launch; `conpty first byte … bytes=N totalBytes=M` shows agent output volume.

---

## 10. Code touchpoints (everything changed this effort; all uncommitted on the worktree)

**Renderer / desktop**
- `desktop/src/renderer/src/components/canvasRenderer.ts` — the canvas renderer (new).
- `desktop/src/renderer/src/components/TermView.tsx` — renderer selection (`auto`/`dom`/`canvas`),
  canvas wiring + `requestPaint()` on write, nudges, `data preview` + `data session mismatch` (throttled,
  diagnostics-gated), `isNudging` guard.
- `desktop/src/main/index.ts` — pre-ready flags (occlusion/direct-composition/disable-gpu), native nudge,
  `capturePixelProbe` (terminal-rect), `cs:get-render-info` / `cs:force-repaint` / `cs:set-terminal-rect`
  IPC, gpu-status logging.
- `desktop/src/main/settings.ts` — settings: `terminalRenderer`, `terminalNudge`, `terminalDiagnostics`,
  `terminalRenderSelfTest`, `disableWindowOcclusion`, `disableDirectComposition`, `disableGpuCompositing`,
  `resumeAgentSessions` (writes `disable_agent_resume` to config.json).
- `desktop/src/main/render-detect.ts` — `isSoftwareCompositing`, `mergeDisableFeatures`, `isRemoteSession`.
- `desktop/src/preload/index.ts` — `getRenderInfo` / `forceRepaint` / `onTerminalNudge` /
  `setTerminalRect` + `RenderInfo` type.
- `desktop/src/renderer/src/components/SettingsModal.tsx` — Diagnostics UI for all the above.

**Go host / config**
- `config/config.go` — `DisableAgentResume` (`disable_agent_resume`).
- `session/winhost/workspace_windows.go` — `create()` gates the `--session-id` seed on
  `!cfg.DisableAgentResume`.
- `session/winhost/conpty_windows.go:201/206/213` — **env handling** (pwsh/powershell:
  `os.Environ()+spec.Env`; **ShellNone (agent): no `cmd.Env`** → inherits host env). *This is the file to
  change if the fix is env injection.*
- `desktop/src/main/host-client.ts:597-602` — spawns `cs.exe session-host` with
  `env: { ...process.env }` (the env the agent ultimately inherits).

**Tests added:** `canvasRenderer.test.ts`, `canvasRenderer.paint.test.tsx`, plus settings/render-detect
test additions. Docs: `docs/rdp-blank-terminal.md` (findings/design), this handoff.

---

## 11. Build / test / install (from repo root)

```bat
:: Go core (the cs.exe / session-host)
go build -o dist\cs.exe .
go test ./config/... ./session/winhost/... ./session/agentcmd/...
gofmt -l config session\winhost            :: must print nothing

:: Desktop app
cd desktop
npm install
npm run typecheck
npm run lint
npm run test            :: vitest (currently 142 passing)
npm run build           :: electron-vite
npm run dist            :: electron-builder --win nsis (installer)
```
The desktop app needs a current `cs.exe` (default `dist\cs.exe`; override with `CS_EXE`). Install the
NSIS output on the box. **Always confirm the installed build's version in `desktop.log`
(`hangar-desktop/<version>`) — a stale 1.6.x install won't have any of this.**

---

## 12. The single most important next action

Run **Test A** (run `copilot` in Hangar's pwsh shell pane) and **Test C** (`default_shell: pwsh`). Those
two, in minutes, will tell you whether the agent hang is the missing profile/proxy environment — the
current leading and only-unruled-out hypothesis. Capture `~/.hangar/host.log` + `desktop.log` (diagnostics
on) for whatever you try.
