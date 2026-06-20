# Blank terminal panes on no-GPU / RDP machines — findings & implementation plan

> **Note (2026-06-20):** This doc covers the **rendering** problem (Chromium not presenting xterm on
> software compositing), whose fix — the 2D-canvas renderer — is correct and shipped. A **separate**
> blank-pane symptom on Windows (AI-agent panes specifically) was later root-caused to an unrelated
> **VT emulator reply-pipe deadlock** in the session host, fixed by `pumpEmuReplies()`. If you are
> chasing a *blank agent pane*, read **`docs/rdp-blank-terminal-postmortem.md`** first.

> **Status:** investigation complete; implementation not yet started.
> **Scope:** Hangar desktop app (`desktop/`) terminal rendering on Windows RDP / VDI / no-GPU hosts.
> **Audience:** maintainers deciding what to build, and reviewers (human or AI) evaluating the plan.
>
> This document consolidates **four independent multi-agent investigations** of the same bug, records
> **what was researched and how**, presents the **full effort-ordered catalog of options**, and ends
> with an **opinionated, phased implementation plan** that gates the contested and heavy work behind
> cheap on-box measurement. All code touchpoints were re-verified against the working tree
> (desktop **v1.7.0**) and use current line numbers.

---

## 1. TL;DR

- On a corporate Windows machine over **RDP with no hardware GPU** (Chromium pure **software
  compositing**), the Hangar desktop app paints its React UI normally, but embedded **xterm terminal
  panes render blank**. The data path is provably healthy (bytes arrive, `term.write()` completes,
  the container has a real size, no JS errors). **Only resizing the OS window makes the content
  appear.**
- Refined diagnosis (**genuinely open — deferred to the Phase-0 measurement in §8**): the symptom is a
  **software-compositing present/flush failure**, but two mechanisms remain in contention — **H2**, the
  compositor not presenting xterm's DOM layer until a real layout/dimension delta; and **H1**, a
  **native-window present / occlusion** throttle that only an OS-window event wakes. The **React UI
  keeps painting live**, which rules out *whole-window* occlusion — **but not** a present-path miss
  confined to the terminal's layer. Because only an **OS-window resize** is confirmed to repaint (a
  renderer-internal change has **not** been shown to), H1 cannot be demoted below H2 — see §7.2.
- **The recommended path is cheap-first and measured:**
  1. **Phase 0 (ship first, always safe):** disable native-window occlusion via a launch flag, remove
     the dead `webContents.invalidate()` workaround, and add **diagnostics** (a `capturePage`
     pixel-probe + DOM/measurement logging) so the affected box tells us the true root cause.
  2. **Phase 1 (cheap, gated):** `document.fonts.ready` refit; a **native-window re-raster nudge**
     (`setContentBounds` ±1px — the mechanical replica of the only confirmed fix) as the lead, with a
     **renderer-only `fontSize ±1`** nudge as a cheaper but now-unproven A/B arm. Neither sends a PTY
     resize.
  3. **Phase 2 (contested, behind opt-in + on-box proof):** a tri-state renderer/graphics mode and the
     **WebGL addon**; **SwiftShader only** if the probe proves "DOM rows exist but pixels are blank."
  4. **Phase 3 (deferred backstop):** host-side **cell-grid → single `<canvas>`** rendering, which is
     immune to Chromium's renderer quirks by design.
- A **separate, rendering-independent** "difficulty connecting" bug (zero-data panes during rapid
  switching) is tracked as a parallel secondary track.

---

## 1a. Update — on-box evidence (the failure is the native present path, H1)

A Phase 0/1 build (occlusion-disable + nudges + diagnostics) was tested on the real RDP/no-GPU box
(`hangar-desktop/1.7.0`, `softwareCompositing:true`, `remoteSession:true`, `webgl:disabled_off`,
`windowOcclusionDisabled:true`). **Result: still blank.** Disabling `CalculateNativeWinOcclusion`,
switching the nudge to `fontsize`, and two manual Force-repaints all failed to make the terminal appear.

**Decisive signal:** the `capturePixelProbe` showed a **populated, live, *changing*** composited surface
while the screen stayed visually blank — attach `sampledNonBackgroundPixels:162982`; two force-repaints
`337042` with *differing* checksums. Per the §8 decision table this is the **native present-path /
occlusion family (H1)** signature: the renderer **is** rasterizing into Chromium's composited surface,
but that surface is **never presented** to the RDP display — and neither the occlusion flag nor a
window/`fontSize` nudge forces a present. This down-weights H2 (compositor-raster) for this box.

**Independent hardware corroboration (user's on-box analysis):** the RDP display adapter reports
**MPO = Not Supported**, **Hardware Scheduling Off**, DDraw "Not Available" (Microsoft Remote Display
Adapter / Hyper-V Video, no hardware GPU); session is RDP (`SESSIONNAME=rdp-…`). MPO/DirectComposition is
precisely the present path that the iteration-2 lever disables — so the hardware facts independently
point at the same H1 present-path root cause, and at `--disable-direct-composition` as the targeted fix.
*(The same analysis confirmed ConPTY itself is healthy — `copilot` runs interactively in a real
pseudo-console — so the blank pane is purely a Chromium present issue, not a data/ConPTY problem.)*

**Caveat:** that probe sampled the **whole window** (including the always-painting React UI), so it could
not yet isolate the terminal region. Iteration 2 fixes this (terminal-rect-only capture) to make H1-vs-H2
unambiguous on the next run.

**Iteration 2 (implemented):**
- **`--disable-direct-composition`** — DirectComposition/MPO is the prime suspect for "composited but not
  presented" over RDP (and this box reports MPO Not Supported), so disabling it is the canonical remedy.
  Gated on `isRemoteSession()` (env-based, pre-ready — no effect on local GPU machines) + a
  `disableDirectComposition` setting (default on, A/B-able). The occlusion set also gains
  `--disable-backgrounding-occluded-windows` + `--disable-renderer-backgrounding`.
- **Terminal-rect-isolated probe** — the renderer reports its pane rect (`cs:set-terminal-rect`) and main
  captures **only that rect**, so the non-background count is the terminal's pixels alone: a large count
  while the screen is blank = **H1 (present)**; ≈0 = **H2 (raster)**.
- **`disableGpuCompositing` (opt-in, default OFF)** — appends `--disable-gpu-compositing --disable-gpu`.
  The research consensus is to *avoid* these (they can entrench the software path / break fallback), so
  they ship **off** and user-toggleable only, as a last-resort A/B lever for this unusual box.

**Out of scope (separate, already-mitigated):** the "Creating…" modal slowness (a create-time PowerShell
profile probe under EDR + OneDrive cold-start; main already bounds it with a 30 s timeout + per-phase
timing) and the agent `exit code 1` (cpa/agency needing a real TTY) are **not rendering bugs** and are not
addressed here.

**If `--disable-direct-composition` does not fix it**, the H1 signature points to **Phase 3 (host-side
cell-grid → single `<canvas>`)** as the durable, present-path-independent fix — not the WebGL/SwiftShader
or single-surface-canvas routes (those address H2).

**Update — on-box result #2 (iter 2/3 build): all Chromium flags exhausted, still blank.** With
`windowOcclusionDisabled`, `directCompositionDisabled`, `gpuCompositingDisabled`, and
`hardwareAccelerationDisabled` **all true**, the pane stayed blank — and the terminal-rect probe read an
identical `sampledNonBackgroundPixels:1202` every run. **No Chromium present-path flag changes anything.**
The decisive new observation: the **React UI presents fine on screen while only the terminal region is
blank** — so this is *not* a whole-window present throttle; it is specific to **xterm's DOM-renderer
layer** (hundreds of absolutely-positioned `<span>`s the RDP software compositor won't present). That
moves the fix from flags toward **getting the terminal onto a single surface**.

**Iteration 4 (implemented) — canvas-viability self-test (decides whether Phase 3 will work).** Before
committing to the host-side canvas rewrite, the doc's Phase-0 canvas-viability test is now shipped: a
`terminalRenderSelfTest` setting (default off) overlays a small **animated 2D `<canvas>`** on the pane and
logs `RenderSelfTest raf {framesPerSec}`. **Canvas animates while xterm is blank → a single-surface
renderer (Phase 3 host-side canvas, no GPU) is the fix → build it.** Canvas also blank → the failure is
below the renderer (region present) and even a canvas won't help → escalate to a non-Chromium display
path. This one observation selects the durable fix before any large build.

**Update — on-box result #3 (iter 4): CANVAS PRESENTS → fix shipped.** The self-test canvas **animated on
screen** in both the agent and shell panes (`RenderSelfTest raf framesPerSec:~32` steady; the terminal-rect
probe rose `1202 → 10721` from the canvas pixels). So a single 2D `<canvas>` presents reliably on this box
where xterm's DOM renderer does not. **The fix is now implemented** as a client-side canvas renderer
(`canvasRenderer.ts` / `CanvasTermRenderer`): it keeps xterm for parsing, the buffer model, input,
selection and scrollback, and repaints the visible cells onto one overlaid opaque `<canvas>` via xterm's
public buffer API. It is selected by a new `terminalRenderer: 'dom' | 'canvas'` setting (default `dom`;
choose **Canvas** on affected machines). This is lighter and lower-risk than the host-side cell-grid
rewrite (no protocol change, reuses xterm's input/selection/scrollback) while achieving the same
single-surface goal. *(The host-side cell-grid remains the heavier fallback if a client-side renderer
proves insufficient.)*

---

## 2. Problem statement

### 2.1 Environment
- Hangar desktop **v1.7.0**: Electron **42.4.1** / Chromium **148** / React **19** / **xterm.js 6.0.0**.
- Terminal uses the **default DOM renderer**; only `@xterm/addon-fit` is loaded
  (`desktop/src/renderer/src/components/TermView.tsx:42-61`).
- Windows native session host; control protocol is **v9** (`session/winhost/proto/proto.go:36`).
- GPU status on the affected box: `softwareCompositing: true`; `gpu_compositing` / `2d_canvas` /
  `rasterization` = `disabled_software`; `webgl` / `opengl` / `webgpu` = `disabled_off`. **No GL
  backend at all.**

### 2.2 Symptom
The React UI (sidebar, tabs, modals) paints and updates normally, but xterm panes are **blank** until
an **OS-window** resize, after which they paint correctly and stay correct.

### 2.3 What is already proven
- **Data path is healthy.** Bytes arrive over the attach pipe, `term:data` IPC fires, and
  `term.write()`'s completion callback runs. The app already logs byte accounting via a `diag()`
  helper to `~/.hangar/desktop.log` so a blank pane can be diagnosed without DevTools (counters at
  `TermView.tsx:76-78`; `TermView first data` / `first write done` diags at `:183-195`; totals at
  `:208` / `:317`). *Caveat: `term.write()` completing proves xterm **parsed** the bytes, not that the
  DOM **painted**.*
- **An OS-window resize fixes it; a renderer-internal change has not been shown to.** All four
  investigations agree an **OS-window resize** repaints the pane. An earlier claim that dragging the
  **pane splitter** also repaints it — which would have implied a *purely renderer-internal* trigger and
  pointed squarely at H2 — is **not confirmed, and is treated as incorrect here.** Consequently we
  **cannot** currently distinguish "a renderer-internal delta is sufficient" (H2) from "an OS-window
  event is required" (H1); the Phase-0 pixel-probe (§8) is what resolves it. *(This retraction directly
  weakens the case for the renderer-only nudge and strengthens the native-window nudge — see §8 Phase 1.)*

### 2.4 Current mitigation code (all ineffective for this bug)
- `app.disableHardwareAcceleration()` gated on a setting (`desktop/src/main/index.ts:71-74`) — a
  **no-op** with no GPU to disable.
- `scheduleTerminalRepaint()` / `forceTerminalRepaintBurst()` calling `webContents.invalidate()`,
  gated on `softwareCompositing` (`index.ts:97-117`, invoked `:266`, `:291`) — **ineffective**:
  `invalidate()` is a paint *request* that is coalesced/ignored under software compositing without a
  `BeginFrame`.
- `softwareCompositing` is resolved **after** `app.whenReady()` from `getGPUFeatureStatus()`
  (`index.ts:787-790`; detector in `desktop/src/main/render-detect.ts`).
- There is **no `app.commandLine.appendSwitch(...)` anywhere** — launch flags are a new addition and
  must be applied **before `app.whenReady()`**.

---

## 3. How this was investigated (what was researched)

Four independent investigations were run (by separate agents/sessions), each producing a findings doc;
then **three sub-agents across distinct model families** (GPT-5.5, Gemini, Claude Opus 4.8) each
independently re-read all four and produced an effort-ordered consolidation, which was merged into this
document. the four source investigations are referred to below as **D1–D4**. (The four findings docs are
external session artifacts, **not committed to this repo**; the cross-doc "consensus" counts in §6–§7
are drawn from them. Every *codebase* claim in this doc was independently re-verified against the
working tree.)

Each investigation decomposed the problem into parallel research threads across model families:

| Thread | Question | Representative models used |
| --- | --- | --- |
| **Electron/Chromium flags** | Which launch switches change software-compositing/occlusion behavior on RDP? | GPT-5.4 / GPT-5.5, Gemini |
| **xterm renderers** | What renderer options exist on xterm 6 (canvas/WebGL/DOM)? Compatibility & context requirements. | Claude Sonnet, Gemini |
| **Prior art** | How do VS Code / Hyper / Tabby / code-server render terminals under RDP/no-GPU? | Claude, Gemini |
| **Codebase feasibility** | Exact integration points, effort/risk, blast radius, the Go host's capabilities. | Claude (general-purpose / Opus) |
| **Diagnosis critique** | Pressure-test the root cause; propose cheap disambiguating experiments. | GPT-5.5 (rubber-duck) |
| **Plan review** | Independently critique the synthesized plan. | GPT-5.5, Gemini, Claude Opus |

**Key external references surfaced (verify on the target box before relying):**
- Chromium SwiftShader requires `--enable-unsafe-swiftshader` for WebGL on no-GPU
  (`chromium/.../docs/gpu/swiftshader.md`); the flag is security-gated/enterprise-temporary in 137+.
- `CalculateNativeWinOcclusion` RDP blanking: Electron #29344/#42378, Chromium #1101748; **VS Code
  ships `--disable-features=CalculateNativeWinOcclusion` by default**.
- xterm canvas addon removed in **xterm 6.0.0 (PR #5105)**; `@xterm/addon-canvas@0.7.0`/`0.8.0-beta`
  peer `@xterm/xterm@^5`. `@xterm/addon-webgl@0.19.0` is xterm-6-native (co-released, shares gitHead
  with `@xterm/xterm@6.0.0`).
- VS Code WebGL→DOM fallback pattern (`xtermTerminal.ts`: "Webgl could not be loaded. Falling back to
  the DOM renderer").
- `charmbracelet/x/vt` cell/damage APIs for the host-side option (`vt/screen.go` `Touched()`,
  `vt/damage.go`).

---

## 4. Refined diagnosis

A manual resize simultaneously wakes the native window present, Chromium layout, xterm's
`ResizeObserver` → `fit()`, the host resize, and possibly an agent redraw — so by itself it does not
implicate one mechanism. Three hypotheses fit the evidence:

| # | Hypothesis | Why it fits | Status across docs |
| --- | --- | --- | --- |
| **H1** | **Native-window occlusion / present throttling.** Chromium's `CalculateNativeWinOcclusion` false-positives the RDP window as occluded and pauses presents; a resize is one of the few OS events that wakes it. | Explains why `invalidate()` and DOM toggles fail and why **only an OS-window resize** is confirmed to repaint. | **In contention (not demoted).** The live React UI rules out *whole-window* occlusion, but **not** a present-path miss confined to the terminal's layer. With the pane-resize datum retracted, only an OS-window event is confirmed to repaint — consistent with a native-present trigger. |
| **H2** | **Software-compositor flush needs a real dimension/structure delta.** xterm's incremental DOM text mutations are not promoted to a new raster; only re-creating per-row spans / re-measuring cells forces a present. | All prior fixes were paint-only and changed neither dimensions nor DOM structure. *(Note: the previously-cited supporting datum — a renderer-internal pane resize repainting — has been **retracted**, see §2.3.)* | **Leading in the source docs (~70–75% in the doc that commits), but weaker now** that its key behavioral evidence is retracted; co-equal with H1 pending the Phase-0 probe. |
| **H3** | **xterm cell measurement / font timing.** Cells measured `0×0` before the Nerd Font loads → rows have no height until a refit. | The app fits after rAF but does not await `document.fonts.ready`. | **Minor / cheap to exclude** (~20–25%). |

**Why each prior fix failed (and what it rules out):**
- `disableHardwareAcceleration()` — no-op (no GPU); also **conflicts** with a future SwiftShader path.
- `webContents.invalidate()` — a paint request only; coalesced/ignored under software compositing /
  occlusion; never forces a present.
- (Historical, **not in this tree**) a same-tick `display:none→''` + `term.refresh()` toggle — a
  synchronous toggle may never commit a hidden state to the compositor; identical DOM nodes ⇒ "nothing
  to raster."

The discriminating question — *what does a real resize do that a DOM toggle does not?* — points at the
**present path** and **re-measurement after fonts load**, both cheaply testable (see §8 Phase 0).

---

## 5. Ruled out / corrected vs. the original investigation

| Candidate | Verdict | Why |
| --- | --- | --- |
| **`@xterm/addon-canvas`** (2D canvas renderer) | **No drop-in on xterm 6** | Removed in xterm 6.0.0 (PR #5105); latest packages peer `@xterm/xterm@^5`. Requires vendoring the addon to xterm 6 or pinning xterm 5.x. |
| **SwiftShader as the obvious fix** | **Contested** | D1 high-confidence; D4 a cheap probe; D2 contingent; **D3 rejects it as a "trap"** (CPU-heavier than DOM for text, regresses healthy machines, security-gated temporary flag). Treat as opt-in, measured. |
| **More paint-only nudges** (`invalidate`/`refresh`/`display`-toggle) | **Rejected** | No dimension/structure delta; proven insufficient. |
| **`--disable-gpu` / `--disable-gpu-compositing` / `--disable-software-rasterizer` / `--in-process-gpu`** | **Avoid** | Entrench the bad software path or hard-conflict with SwiftShader. |
| **Electron OSR-to-canvas / custom hand-rolled renderer** | **Rejected** | High effort; OSR still depends on the GPU pipeline being avoided; no advantage over WebGL/host-grid. |
| **Protocol "v3"** (claim in an earlier note) | **Corrected to v9** | `session/winhost/proto/proto.go:36` `const Version = 9`. |
| **Agent `copilot.exe` TTY exit-1** | **Out of scope** | An agency/wrapper issue, not Hangar. |

---

## 6. Options — effort-ordered catalog

Effort: XS = a few lines/flags · S = localized change · M = spike / new dep + lifecycle · L–XL =
architecture. "Docs" = how many of D1–D4 propose it.

| # | Option | Effort | Fixes blank? (confidence) | Docs | Key risks / preconditions |
| --- | --- | --- | --- | --- | --- |
| 1 | **`--disable-features=CalculateNativeWinOcclusion`** before `app.whenReady()` | XS | Med–High (may fix outright; "hardening" per the H2-committed docs) | **4/4** | Merge-safe append (don't clobber other `--disable-features`); decide always-on vs RDP-gated. |
| 2 | `--disable-backgrounding-occluded-windows` + `--disable-renderer-backgrounding` | XS | Low–Med | 2/4 | Companion "occlusion set"; bundle with #1. |
| 3 | `--disable-direct-composition` (separate toggle) | XS | Med ("most reliable single RDP flag" — D2); **leading lever after on-box H1 evidence (§1a)** | 1/4 | **Implemented (iter 2)**, gated on `isRemoteSession()` + setting. Test in isolation for attribution. |
| 4 | **Remove the dead `webContents.invalidate()` workaround** (`index.ts:97-117,266,291`) | XS | 0% as a fix (cleanup; saves RDP CPU) | 2/4 explicit | Remove only once a replacement exists. |
| 5 | **Guard-rail:** avoid `--disable-gpu*` / `--disable-software-rasterizer` / `--in-process-gpu` | XS | — | 3/4 | These hurt on RDP. |
| 6 | **SwiftShader probe** `--use-gl=angle --use-angle=swiftshader --enable-unsafe-swiftshader` (+ Win `--disable-features=AllowD3D11WarpFallback`) | XS | **CONTESTED** (D1 high · D4 probe · D2 contingent · **D3 trap**) | 3/4 yes, 1/4 no | **Must NOT call `disableHardwareAcceleration()`** (mutually exclusive); enterprise-gated; trusted-content only. |
| 7 | **Diagnostics build** — `capturePage(termRect)` pixel-count/hash + `.xterm-rows`/`onRender`/`MutationObserver` + paint-matrix + animated-`<canvas>` overlay | XS–S | n/a — decides the mechanism | 2/4 strong | Log counts/hashes only (never screenshots); `term._core` guarded, diagnostics-only. |
| 8 | `document.fonts.ready` await before `fit()` + one `fit()+refresh()` after first write | XS–S | Med-low (targets H3) | 1/4 (+1 names H3) | Near-zero risk. |
| 9 | rAF "heartbeat" periodic repaint | XS–S | Low (band-aid) | 2/4 | Stop after first confirmed non-blank frame. |
| 10 | **Renderer-only `fontSize ±1` (or `cols ±1`) nudge** across 2 rAFs | S | **Unproven** (was ~70–75% per D3, but that rested on the now-retracted pane-resize datum) | 1/4 primary | **MUST NOT send a PTY/host resize**; add an `isNudging` guard; stagger across rAF or it coalesces; gate on `softwareCompositing`; ≤2 fires; config `terminalNudge`. |
| 11 | Frame-stable **native window** nudge (`setContentBounds` +1px held ~2 rAF then restore) | S | **Med-high — now the leading nudge** (mechanical replica of the only confirmed fix, an OS-window resize) | 1/4 | One-shot + 1s retry; skip maximized/fullscreen; must be frame-stable; guard host `Resize`. |
| 12 | Manual **"Force terminal repaint"** command | S | n/a (escape hatch) | 2/4 | Should not resize the PTY. |
| 13 | RDP/remote-session **auto-detection** in `render-detect.ts` (`SESSIONNAME` / `SM_REMOTESESSION`) + persisted render-state cache | S | n/a (gating infra) | 2/4 | `SESSIONNAME` heuristic; back with cache + env override. |
| 14 | Tri-state **graphics/renderer mode** + UI override (migrate the `disableHardwareAcceleration` boolean) | S–M | n/a (enabler + escape hatch) | 3/4 | Axis disagreement: Chromium backend (incl. SwiftShader) vs xterm renderer. |
| 15 | **`@xterm/addon-webgl@0.19.0`** + WebGL→DOM fallback (VS Code pattern) | S–M | **CONTESTED** (primary per D1 ≈85% · optional/contingent per D2/D3/D4) | 4/4 (role disputed) | Needs a WebGL2 context the box lacks → **coupled to #6**; subscribe `onContextLoss`→dispose; dispose addon before `term.dispose()`. |
| 16 | **Single-surface 2D canvas** — vendor `addon-canvas` to xterm 6 **or** pin xterm 5.x + `addon-canvas@0.7.0` | M | ★★★★ durable (D4) **vs** dead end (D1/D3) | 1/4 champions | No v6 build; spike not a drop-in; canvas also uses rAF → validate with #7 first; loses v6 features if pinned. |
| 17 | Electron version **bisect/downgrade** (diagnostic) | M | Low — diagnostic only | 1/4 | CVE exposure; D1 "do not downgrade"; D2 deprioritized. |
| 18 | **Host-side cell-grid → single client `<canvas>`** (proto **v9→v10**) | L–XL | ★★★★★ immune by design | 3/4 | Reimplements selection/scrollback/mouse outside xterm; deferred backstop. |

---

## 7. Consensus & open disagreements

### 7.1 Strong consensus
1. `@xterm/addon-canvas` has **no xterm-6 build** (removed in 6.0.0); adopting 2D canvas requires
   vendoring or pinning xterm 5.x.
2. **Data path is healthy**; only an **OS-window** resize repaints. (`write()` completing ≠ painted.)
3. `disableHardwareAcceleration()` is a **no-op** with no GPU and is **mutually exclusive with
   SwiftShader**.
4. `webContents.invalidate()` is **ineffective** under software compositing.
5. The bug **only reproduces on a real RDP/no-GPU box**; verify via `desktop.log`.
6. `--disable-features=CalculateNativeWinOcclusion` is cheap RDP hardening that **VS Code ships**.
7. **Avoid** `--disable-gpu*`, `--disable-software-rasterizer`, `--in-process-gpu`.
8. Ground truth: desktop **v1.7.0**, default DOM renderer + only FitAddon, **no `appendSwitch` yet**,
   flags must go before `app.whenReady()`.
9. Host control protocol is **v9**.
10. Prior art = **WebGL→DOM fallback** (VS Code/Hyper/Tabby/code-server); none ship canvas on xterm 6;
    the recommended RDP/no-GPU fallback is the **DOM renderer**.
11. The control-pipe "difficulty connecting" wedge is a **real, separable** bug.

### 7.2 Open disagreements (the decisions this plan deliberately defers to measurement)
- **Root cause weighting:** H2 (compositor-flush) vs H1 (native present/occlusion) vs H3 (fonts) — now
  treated as **genuinely open**. The original H2 lean partly rested on a "renderer-internal pane resize
  repaints" datum that has since been **retracted** (§2.3), so H1 is no longer demoted. The surviving
  argument — **React paints live ⇒ not *whole-window* occlusion** — still holds, but it does **not**
  rule out a present-path miss confined to the terminal layer (a flavor of H1). Resolved by the Phase-0
  probe, not by prior.
- **Diagnostics-first vs ship-fixes-instrumented.** *(This plan does both: ships always-safe fixes
  while instrumenting.)*
- **SwiftShader:** recommended vs "trap." → gated behind opt-in + on-box proof.
- **WebGL role:** primary durable fix vs optional/contingent. → Phase 2, gated.
- **Nudge flavor:** renderer-only `fontSize/cols ±1` vs native `setContentBounds`. **All agree: never
  send a PTY resize.**
- **Rollout:** one bundled build vs iterative A/B. → instrumented + independently toggleable.

---

## 8. Conclusion — recommended phased implementation plan

Principle: **ship the always-safe, zero-/low-cost fixes immediately while instrumenting the box, and
gate every contested or heavy item behind a decision signal from `desktop.log`.** Each mitigation is
independently toggleable so a single field build is attributable.

### Phase 0 — Instrument + zero-code hardening (ship first; safe on every machine)
1. **Disable native-window occlusion.** Add, before `app.whenReady()` (near the existing pre-ready GPU
   block in `index.ts`), a **merge-safe** `app.commandLine.appendSwitch('disable-features',
   'CalculateNativeWinOcclusion')` that preserves any other `--disable-features` values. Optionally add
   `--disable-backgrounding-occluded-windows` + `--disable-renderer-backgrounding`. Gate via a setting
   (`disableWindowOcclusion`, default on) + env override.
2. **Retire the dead `invalidate()` workaround** (`index.ts:97-117`, calls `:266`/`:291`) — first
   **disable** it (it is already gated on `softwareCompositing`), and **remove it only once the Phase 1
   nudge is validated** on the box, so a mitigation is never deleted before its replacement is proven.
   Keep the `softwareCompositing` resolution and logging.
3. **Diagnostics (the decision-maker).** Add, gated behind a dev flag and logged to `desktop.log`:
   a main-process `webContents.capturePage(terminalRect)` **non-background-pixel count/hash** probe; a
   renderer DOM/measurement burst (`.xterm-rows` count + first-row text length + rects + computed
   styles, guarded `term._core` cell w/h, `term.onRender` counter, `MutationObserver`,
   `document.visibilityState`, rAF intervals) at mount / first-write / +250ms / +1s / after-resize; a
   **paint-mechanism matrix** (`term.refresh` vs same-size `fit.fit()` vs `term.resize` vs a real
   `BrowserWindow.setSize`); and a **plain animated `<canvas>` overlay** viability test.
4. **Detection + plumbing.** Extend `render-detect.ts` with an RDP/remote detector and add a persisted
   render-state cache (so the next launch can gate pre-ready flags), plus a preload `getRenderInfo()`.
   Unit-test the new detector alongside the existing electron-free helpers (e.g. `render-detect.test.ts`).

**Note on the probe:** `webContents.capturePage()` reads the renderer's **composited surface**, not the
pixels actually presented on screen, and the capture itself may perturb paint — so log **counts/hashes
only**, throttled and one-shot. A *pure native-present/occlusion* failure (H1) can therefore show
`capturePage` = "pixels present" while the user still sees blank; that combination (pixels in the
capture **but** the screen is visually blank until a resize) is the true H1 signature.

**Decision signals (read one `desktop.log` from the box):**

| DOM rows populated? | `capturePage` shows pixels? | Conclusion → next phase |
| --- | --- | --- |
| Yes | No | Inside Chromium (compositor raster) → Phase 1 nudge; escalate to Phase 2/3 if needed |
| Yes (capture) but **screen blank until resize** | Pixels present in the capture | **Native present / occlusion (H1)** → Phase 0 flags + Phase 1 native nudge |
| No / `0×0` cells | n/a | Font/measurement (H3) → Phase 1 `fonts.ready` refit |

### Phase 1 — Cheap targeted renderer fixes (gated on Phase 0)
1. **`document.fonts.ready`** before `fit()`, then one `fit()+term.refresh()` shortly after first
   write (addresses H3 at near-zero risk; can be on always under software compositing).
2. **Native-window re-raster nudge (primary — it replicates the only confirmed fix).** Because only an
   OS-window resize is confirmed to repaint, hold a frame-stable `setContentBounds` +1px for ~2 rAF /
   150 ms then restore; one-shot + one ~1s retry if the probe still reads blank; skip when
   maximized/fullscreen; **guard the host `Resize`** so this never propagates a PTY resize. Prefer this
   when the Phase-0 signal is "native present (H1)."
3. **Renderer-only re-raster nudge (cheaper, but now unproven).** In `TermView.tsx`, toggle
   `term.options.fontSize` by ±1 across two `requestAnimationFrame`s, then restore. **Must NOT call
   `window.cs.resize`/host resize** (`TermView.tsx:172` is that host-resize call — it would SIGWINCH the
   alt-screen agent and race the ResizeObserver/fit logic). Guard with an **`isNudging` flag** so the
   temporary `fontSize` change does not re-enter the `ResizeObserver`→`fit()` path and recurse. Stagger
   across rAF boundaries (or Chromium coalesces it); gate on `softwareCompositing`; **bound to ≤2 fires**
   (post-attach + first `term:data`); emit `diag('nudge fired')`. Config `terminalNudge: off|fontsize|cols`
   + env `HANGAR_NUDGE`. *Note: with §2.3 retracted, a renderer-internal nudge is no longer
   evidence-backed — keep it as a cheap A/B arm, not the lead.*
4. **Manual "Force terminal repaint"** command wired to the nudge — a user escape hatch and self-report
   aid.

### Phase 2 — Renderer mode + WebGL (contested; behind explicit opt-in + on-box proof)
- Replace the `disableHardwareAcceleration` boolean with a **tri-state** setting (migrate the old
  value) and surface it in Settings UI. Choose the axis deliberately: a **graphics mode**
  (`auto|software|swiftshader`) toggles the Chromium backend; a **renderer mode**
  (`auto|webgl|dom`) toggles xterm. (Recommend exposing renderer mode to users; keep SwiftShader as an
  advanced/diagnostic option.)
- Add **`@xterm/addon-webgl@0.19.0`**; load after `term.open()` with a `getContext('webgl2')`
  pre-flight and `onContextLoss → dispose()` (reverts to DOM); dispose the addon before
  `term.dispose()`. On software compositing, `auto` resolves to **DOM**.
- **SwiftShader only** if the Phase-0 probe proves *DOM-rows-but-blank-pixels* **and** the user opts in
  (corporate GPO may block it). In SwiftShader mode, **do not** call `disableHardwareAcceleration()`.
- **Tests + migration:** unit-test the tri-state settings migration (old `disableHardwareAcceleration`
  boolean → new mode) alongside the existing `desktop/src/main/__tests__/settings.test.ts`, and cover
  the WebGL load + `onContextLoss`→DOM fallback path.

### Phase 3 — Durable backstop (deferred; only if Phases 0–2 fall short)
- **Host-side cell-grid → single client `<canvas>`.** The Go host runs `charmbracelet/x/vt` per console
  and **already iterates cells** for scrollback ANSI (today via full render + hashing). The `vt`
  emulator exposes cell/damage APIs (`CellAt`/`ScrollbackCellAt`, `CursorPosition`, `Width`/`Height`,
  `IsAltScreen`, and `Touched()`/`ClearTouched()` damage tracking) — **but the host does not use the
  damage-tracking APIs yet** (no `Touched()` usage exists in `session/winhost`), so per-line diffing is
  **new work**, not a wiring-up of existing calls. Add a `CaptureGrid`/`AttachGrid` (or
  `GetScreen`/`ScreenState`) message streaming styled cells + `Touched()` line diffs; bump protocol
  **v9 → v10**; client paints one canvas (`fillRect`+`fillText` per dirty cell). Immune to Chromium's
  renderer by design, but reimplements selection/scrollback/mouse-SGR/copy outside xterm; keep
  desktop↔daemon protocol in sync.

### Parked unless the box demands it
- **2D canvas renderer** (vendor `addon-canvas` to xterm 6, or pin xterm 5.x) — only if the canvas
  overlay paints in Phase 0 *and* WebGL/SwiftShader is rejected.
- **Electron downgrade/bisect** — last-resort diagnostic only (CVE exposure).

### Secondary parallel track — control-pipe "difficulty connecting" wedge
Rendering-independent; produces **zero-data panes that look blank for a different reason** during rapid
workspace/shell switching.

**Mechanism (verified in code):** the host dispatches each control-pipe connection's RPCs **serially**
(`session/winhost/host_windows.go:129` `handleConn`). The `KillSession` RPC (`:287`) calls
`killSession` (`:403`) whose `s.close()` (`:411`) blocks **up to ~3.5 s (worst case)** — `gracefulExitWait`
500 ms + `procExitWaitTimeout` 3 s (`session/winhost/conpty_windows.go:38,43`; `close()` `:838-869`),
and only when the child neither exits on ConPTY close nor within the kill wait. Under rapid switching,
queued kills **head-of-line-block a following `Attach`** on the same connection; the `Attach` is
rejected only after the **120 s** client timeout (`desktop/src/main/host-client.ts:289`
`DEFAULT_CALL_TIMEOUT_MS = 120_000`), reached **cumulatively** when several queued kills sit ahead of
it. The pane then shows an attach-error line (`TermView.tsx:277-278`) if paint works, or stays blank.

**Critical correction:** a naive "make `killSession` async" is a **bug** — `killSession` is **shared**
by four internal callers in `session/winhost/workspace_windows.go`: **archive** (`:1443`, which then
deletes the worktree on a tight retry budget), **regenerate** (`:1159`/`:1162`), and **dead-session
cleanup** (`:612`). A 3.5 s async close would outlast archive's delete budget → the still-running
ConPTY holds the worktree handle → **worktree deletion fails**.

**Agreed fix shape:** split `killSession` into a detach (locked map-delete) + `s.close()`; make it
**async only at the `KillSession` RPC site** (`host_windows.go:287`): detach, `go s.close()`, reply
immediately. **Keep all four `workspace_windows.go` callers (`:612`, `:1159`, `:1162`, `:1443`)
synchronous.** Note the desktop **`cs:close-shell`** IPC also routes to the `KillSession` RPC, so the
async site is reachable just before an archive — provide a **synchronous close-and-wait variant** (or
archive-aware ordering) for shell closes that precede archival, so a shell-held worktree handle can't
race the delete. Add a **name-keyed `closing` guard** so a recreate in the same worktree joins any
in-progress close. Optional complement: debounce/coalesce `cs:ensure-shell` on the desktop during switch
storms (`desktop/src/main/index.ts:471-519`). *Effort S–M, risk Low. Fully parallelizable.*

---

## 9. Validation protocol (must run on the affected RDP machine)

Unit tests cannot reproduce this; every mechanism must be observable via `~/.hangar/desktop.log`. For
each build:
1. Confirm baseline: `softwareCompositing: true`, `TermView mount` + byte-accounting show bytes arrived
   and `term.write` ran ⇒ data path fine, paint bug confirmed.
2. Read the Phase-0 **decision signal** table (§8) to attribute H1 vs H2 vs H3.
3. Open a workspace **without resizing** — **pass = terminal paints immediately.**
4. Toggle mitigations independently (occlusion flag only → nudge only → fonts-refit only) to isolate
   the responsible change.
5. Test RDP disconnect/reconnect (WebGL context loss in Phase 2) — confirm graceful DOM fallback.
6. Wedge: rapid switching → `KillSession` returns immediately, no 120 s `Attach` timeout, new pane logs
   first data; then archive-with-delete → worktree actually removed (no handle race).

---

## 10. Appendix — verified codebase touchpoints (working tree, desktop v1.7.0)

**Desktop (renderer):**
- `desktop/src/renderer/src/components/TermView.tsx:42-61` — `new Terminal({...})` (DOM renderer,
  `fontSize: 13`, `allowProposedApi`), `FitAddon`, `term.open()`.
- `…/TermView.tsx:76-78` — byte-accounting counters; `:172` host-resize call (`window.cs.resize`);
  `:183-195` `TermView first data` / `first write done` diags; `:208`/`:317` totals;
  `:277-278` attach-error line written to the terminal.

**Desktop (main):**
- `desktop/src/main/index.ts:71-74` — `app.disableHardwareAcceleration()` (setting-gated).
- `index.ts:90` — `softwareCompositing` flag; `:97-117` — `scheduleTerminalRepaint` /
  `forceTerminalRepaintBurst` (`webContents.invalidate()`); invoked `:266`, `:291`.
- `index.ts:787-790` — `softwareCompositing = isSoftwareCompositing(...)` after ready.
- `index.ts:471-519` — `cs:ensure-shell` handler.
- `desktop/src/main/settings.ts:30,50,63,206,235-236` — `disableHardwareAcceleration` boolean
  (+ test `__tests__/settings.test.ts:159-165`).
- `desktop/src/main/render-detect.ts` — `isSoftwareCompositing`.
- `desktop/src/main/host-client.ts:289` — `DEFAULT_CALL_TIMEOUT_MS = 120_000`.

**Go host:**
- `session/winhost/proto/proto.go:36` — `const Version = 9`.
- `session/winhost/conpty_windows.go:38` `procExitWaitTimeout = 3s`; `:43` `gracefulExitWait = 500ms`;
  `close()` `:838-869` (graceful-wait select ~`:858`, proc-exit-wait select ~`:865`; worst case ~3.5 s).
- `session/winhost/host_windows.go:129` `handleConn` (serial dispatch); `:287` `MethodKillSession`;
  `:403` `killSession` (`s.close()` `:411`).
- `session/winhost/workspace_windows.go` — internal `killSession` callers, all must stay synchronous:
  `:612` (dead-session cleanup), `:1159`/`:1162` (regenerate), `:1443` (archive).

**Dependencies (`desktop/package.json`):** `@xterm/xterm@^6.0.0`, `@xterm/addon-fit@^0.11.0` (no
webgl/canvas addon), `electron@^42.4.1`.

---

*Source investigations consolidated here: D1 `rdp-blank-terminal-findings-and-plan.md`,
D2/D3/D4 `…-findings-and-recommendations.md` (four independent sessions), plus a 3-model
(GPT-5.5 / Gemini / Claude Opus 4.8) consolidation pass.*
