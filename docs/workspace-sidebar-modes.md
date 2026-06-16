# Feature: Workspace Sidebar Modes, Search & Motion

> Status: Reviewed (incorporates feedback from a 3-model review: GPT-5.5, Gemini 3.1 Pro, Claude Opus 4.8)
> Area: `ui/list.go`, `app/app.go`, `keys/keys.go`, `session/instance.go`, `session/storage.go`, `ui/menu.go`, `config/state.go`
> Related current behavior: the left sidebar ("Instances" list) renders a flat, manually-ordered list of `session.Instance` items. Order is changed only by the user with `K`/`J` (move up/down) and persisted as slice order.

## 1. Summary

Add richer organization to the left workspace sidebar:

1. **View modes** — a single cycling control that switches how workspaces are ordered/grouped:
   - **Manual** (today's behavior; reorder with `K`/`J`).
   - **Group by repo** — workspaces clustered under a per-repository header.
   - **Recent activity** — sorted by most-recent observed agent activity, newest on top.
   - **Pinned-pending** — keeps the manual order, but lifts workspaces that are waiting on the user into a separate pinned section at the top.
2. **Search/filter bar** — press `/` to type a query that filters workspaces by **title** or **repo path**, live, within the active mode.
3. **Motion** — lightweight, frame-based animation of the **sidebar rows** (not the preview/terminal pane) so workspaces visibly emphasize when they reorder, group, filter, or get pinned.

Modes are **mutually exclusive** and cycled with one key. Search is **orthogonal**: it filters whatever mode is active.

## 2. Review synthesis (what changed after multi-model review)

Three models independently reviewed the first draft. The high-consensus findings — and the resolutions now baked into this spec — are:

| # | Finding (consensus) | Resolution in this spec |
| --- | --- | --- |
| R1 | **"Pending" cannot be raw `hasPrompt`.** `hasPrompt` means "a known AutoYes-style approval prompt is on screen," not "waiting on the user," and AutoYes may auto-resolve it. | Define pending via a dedicated **`IsWaitingForUser()`** signal that excludes Paused/Loading and AutoYes-resolved prompts (§4, §6, §7.5). Do **not** pin on raw `hasPrompt`. |
| R2 | **Recent-activity will thrash.** `HasUpdated()` hashes the visible screen every 500ms, so streaming agents flip positions every tick; `time.Now()` per result gives artificial ordering. | Honest naming ("last observed change"), **one batch timestamp per tick**, and an **anti-thrash rule** (minimum dwell + don't reorder a row while it's actively `Running`/selected) (§4, §5.1, §7.5). Long-term: backend-maintained activity timestamp. |
| R3 | **Selection must be identity-based, and identity operations must be explicit.** Create/cancel/start-failure/kill/attach/reorder must target an exact `*Instance`, never "whatever is visible," or filtering can kill/attach the wrong workspace. | New explicit `List` API (§7.2): `SelectInstance`, `SelectNewInstance`, `RemoveInstance`, `KillSelected`, `VisibleCount`, `HasVisible`; `NumInstances()` stays **canonical**. Every call site enumerated (§7.2, §7.10). |
| R4 | **Animation tick is a double-scheduling hazard** and the **slide design isn't feasible** for adjacent multi-line rows. | Animator is a **separate, timer-free type**; one **`animating` flag + generation ID**; stale ticks no-op; recompute **retargets** without spawning a second loop. v1 motion = **highlight-pulse + crossfade**, not per-row pixel slide (§5.4, §7.7). |
| R5 | **Search input conflicts with nav keys and existing menu/key plumbing.** `j`/`k` as navigation makes words like "project" untypeable; `handleMenuHighlighting` and `Menu.SetInstance` will fight a search state. | During search, **arrows only** navigate; letters are text. Add `stateSearch` to the highlight-suppression path and `StateSearch` to `Menu.SetInstance`'s special-state guard (§5.2, §7.8). |
| R6 | **Filtered-out selection behavior was self-contradictory.** | Store **`preSearchSelection`**; restore it on `Esc`, keep the new visible selection on `Enter`; while the desired selection is hidden, preview shows the empty state (§5.2, §7.2, §7.8). |
| R7 | **Repo grouping must key on repo path, not name.** Existing `repos map[string]int` is name-keyed and merges distinct repos sharing a basename. | Group key = **repo root path**; display label = `RepoName()`, disambiguated by path when basenames collide (§5.1, §7.3, §10). |
| R8 | **`metadataUpdateDoneMsg` rewrite risked regressing the Ready/Running state machine** and **`AppState` needs interface methods, not just a struct field**. | Preserve the exact `if updated / else if hasPrompt / else` branch; add `LastActivityAt`/pending as **orthogonal side-effects** (§7.5). Extend the `AppState` interface with `Get/SetSidebarMode` + validation/back-compat (§6, §7.9). |
| R9 | **List is becoming a god-object.** | Extract a **stateless view-model builder** (`buildView(items, mode, filter, selected) []displayRow`) and a **standalone animator**; `List` shrinks to canonical `items` + `selected *Instance` + delegation (§7.1). |

Verified during review (so a flagged "panic" risk is downgraded): the empty-selection path already exists today — at startup with zero instances, `instanceChanged()` passes a `nil` instance, and `PreviewPane.UpdateContent`, `DiffPane.SetDiff`, and `TerminalPane.UpdateContent` all explicitly handle `instance == nil` with fallback states. The new empty-search-result state reuses that same already-safe path; we keep it as a **regression test**, not a blocker.

## 3. Background & current state

Relevant existing code:

- `ui/list.go` — `List` owns `items []*session.Instance`, an `int selectedIdx`, and `repos map[string]int`. `String()` renders every item in slice order via `InstanceRenderer.Render(instance, idx, selected, hasMultipleRepos)`. `Up()/Down()` move `selectedIdx`; `MoveUp()/MoveDown()` swap slice elements; `Kill()` removes by index; `Attach()` indexes `items[selectedIdx]`.
- `app/app.go` — `home` holds the `*ui.List`. A `state` enum (`stateDefault`, `stateNew`, `statePrompt`, `stateHelp`, `stateConfirm`) gates input. `handleMenuHighlighting` intercepts global keys for menu underlining (skips prompt/help/confirm states only). Two self-chaining tickers seeded once in `Init()`: a 100ms `previewTickMsg` (calls `instanceChanged()`), and `tickUpdateMetadataCmd` (500ms) whose `metadataUpdateDoneMsg` applies `updated`/`hasPrompt` + diff stats. Create/cancel flows append a blank instance, `SetSelectedInstance(NumInstances()-1)`, and on cancel/failure call `m.list.Kill()`.
- `session/instance.go` — `Instance` exposes `Title`, `Path`, `Branch`, `Status` (`Running`, `Ready`, `Loading`, `Paused`), `CreatedAt`, `UpdatedAt`, `AutoYes`, `RepoName()`, and `HasUpdated() (updated, hasPrompt bool)`. `TapEnter()` is a no-op unless `AutoYes`. A freshly created, unstarted instance has an empty `Title` and `RepoName()` returns an error.
- `keys/keys.go` — central keymap. In use: `up/k`, `down/j`, `shift+up/down`, `J/K`, `N`, `enter/o`, `n`, `D`, `q`, `tab`, `c`, `r`, `p`, `?`. Confirmed free: `s`, `S`, `/`.
- `ui/menu.go` — `Menu` + `MenuState` (`StateDefault`, `StateEmpty`, `StateNewInstance`, `StatePrompt`); `SetInstance` preserves only `StateNewInstance`/`StatePrompt` as "special" states.
- `config/state.go` — `AppState` is an **interface** (today only `Get/SetHelpScreensSeen`) backed by a `State` struct serialized to `state.json`.

Two gaps make the new modes non-trivial (both confirmed by reviewers):

- **No maintained, trustworthy "last activity" timestamp.** `UpdatedAt` is set to `time.Now()` only inside `ToInstanceData()` at save time. `HasUpdated()` is a 500ms screen-hash that also trips on spinners, redraws, resize, and input echo — usable but noisy.
- **No "waiting on the user" signal.** `hasPrompt` is an AutoYes-approval heuristic consumed only by `TapEnter()`; nothing records "this workspace needs the human."

## 4. Terminology & definitions

- **Canonical (manual) order** — the user-controlled slice order, persisted, and the source of truth for "Manual" and "Pinned-pending" modes.
- **Display order** — the ordered, possibly-sectioned, possibly-filtered sequence of rows actually rendered. Derived from canonical order + mode + filter.
- **Waiting-for-user (pending)** — a workspace is pending iff it is **started**, **not Paused**, **not Loading**, and the agent is **awaiting human input** and that input will **not** be auto-supplied. Surfaced by a new `Instance.IsWaitingForUser() bool`. v1 derivation: `Status == Ready && hasPrompt && !AutoYes`, cleared immediately after a successful AutoYes/host-side auto-approval. Raw `hasPrompt` is **not** used directly (R1).
- **Last observed change (recent activity)** — the last metadata tick at which `HasUpdated().updated == true`, recorded as a new `LastActivityAt`, using **one timestamp per tick batch** (not per-result `time.Now()`). Subject to the anti-thrash rule (R2). Falls back to `UpdatedAt`/`CreatedAt` when zero. Named "recent activity" but documented as "last observed screen change" so expectations match the signal.

## 5. UX design

### 5.1 View modes (cycling)

`s` cycles forward: **Manual → Group by repo → Recent activity → Pinned-pending → Manual …**; `S` cycles backward. The active mode is shown in the sidebar title (e.g., `Instances · recent`). The mode **persists** across restarts.

| Mode | Ordering | Sections |
| --- | --- | --- |
| Manual | canonical slice order | none |
| Group by repo | grouped by **repo root path**; groups sorted alphabetically by label; within a group, canonical order | one header per repo |
| Recent activity | `LastActivityAt` desc, anti-thrash applied; `CreatedAt` desc tiebreak | none |
| Pinned-pending | `IsWaitingForUser()` workspaces first (canonical order among them), then the rest (canonical order) | "Pending" + "Workspaces" headers |

- **Group by repo** always renders the repo header, even with a single repo (this overrides the existing "show repo only when multiple repos" rule **inside this mode**; other modes keep the current rule). Headers use `RepoName()` as the label; if two active repos share a basename, the label is disambiguated with a trailing path hint.
- **Manual reorder (`K`/`J`)** operates in **Manual mode only**. In any other mode it is a **no-op with a transient hint** ("reorder is only available in Manual mode") — it does **not** auto-switch to Manual (which would scramble the view, per R-consensus).
- **Recent-activity anti-thrash:** a row may move to a new sorted position only after it has been stable for a minimum dwell (e.g., one or two ticks), and a row that is the **selected** row or actively **`Running`** is not reordered out from under the user mid-stream. Exact thresholds are tunable constants resolved in Phase 3 (§12).

### 5.2 Search / filter

- `/` enters **search mode** (`stateSearch`): a one-line input renders at the top of the sidebar (e.g., `🔎 query▏`). Typing filters live.
- Match: case-insensitive substring against **`Title`** OR the **repo path** (`Instance.Path`, and the repo root path / `RepoName()`).
- Filtering happens **within** the active mode (grouped results stay grouped; empty groups/headers are dropped).
- **Key routing while searching (R5):** letters/digits/`backspace`/`space` edit the query; **`↑`/`↓` (arrows only)** move selection — `j`/`k` are treated as text, never navigation. `Enter` commits (keeps the filter applied, returns to `stateDefault` with the current visible selection). `Esc` clears the query, exits search, and **restores the pre-search selection** (R6). All other global actions are suppressed until commit/clear.
- **Selection semantics (R6):** on entering search, `preSearchSelection` is captured. While typing, the visible selection is `preSearchSelection` if it still matches, otherwise the first visible match (or none). When the desired selection is hidden, `GetSelectedInstance()` returns `nil` and the preview shows the standard empty state.

### 5.3 Sections & headers

The renderer supports **section headers** interleaved with workspace rows. Headers are **non-selectable**; navigation skips them. Workspace numbering (the `N.` prefix) is **continuous across the visible workspace order**, ignoring headers (decided — not per-group).

### 5.4 Animations / motion (sidebar rows only)

Terminal frames are full static strings re-rendered each tick; motion is **cell/row-stepped over several frames**, never sub-character. Scope is strictly the **left sidebar list** (not the preview/terminal pane).

**v1 motion primitives (R4):**

1. **Highlight pulse** — the primary primitive. When a workspace changes position (manual `K`/`J`, re-sort, filter, pin/unpin), the moved row(s) render with a brief fading highlight background for N frames so the eye tracks what moved. This is necessary because an adjacent `K`/`J` swap reaches its final string in a single frame and would otherwise look instant.
2. **Crossfade** — for many-rows-change-at-once events (mode switch, large re-sort), the old arrangement transitions to the new one via a short pulse on all changed rows rather than a chaotic simultaneous slide.

True per-row "sliding" across multiple line-heights is **explicitly out of scope for v1** (it would require a virtual canvas with per-line clipping/z-ordering, not just reordering row strings). It is recorded as a possible future iteration with a full rendering spec.

**Engine constraints:**
- Animation is **purely cosmetic**: selection, persistence, and the logical display order update **immediately**; only the rendered decoration/placement interpolates.
- The animator is a **standalone, timer-free type** with an explicit `Step()/Frame()` API (deterministic, unit-testable without wall-clock).
- **Single tick loop:** `home` holds an `animating` flag and a generation ID. A fresh `animTickMsg` Cmd is scheduled **only** on a not-animating→animating transition; every tick carries the generation and stale ticks no-op; mid-animation changes **retarget** the animator without scheduling a second loop; the anim Cmd is batched with existing preview/metadata Cmds.
- **Reduced-motion is one predicate, default-safe:** `animationsEnabled = motionConfig && !terminalTooSmall && visibleCount <= threshold && !reducedMotion`. When false, updates are instant. The "instant" path is the trivially-correct default.

### 5.5 Keybindings

| Key | Action | Notes |
| --- | --- | --- |
| `s` | cycle mode forward | free today |
| `S` | cycle mode backward | free today |
| `/` | enter search | standard |
| `Esc` | clear/exit search, restore pre-search selection | only in `stateSearch`; precedence defined vs. scroll mode (§7.8) |
| `K` / `J` | move up/down | **Manual mode only**; no-op + hint elsewhere |

No existing binding is removed.

### 5.6 Menu / help updates

- `ui/menu.go`: add `s sort` and `/ search` to the default instance menu group; add a `StateSearch` menu state showing `esc clear · enter apply`; add `StateSearch` to the set of states `SetInstance` will not override (R5).
- Help overlay (`app/help.go`) documents modes, search, and the reduced-motion toggle.

### 5.7 Empty & edge states

- No workspaces: unchanged empty state; mode/search keys are no-ops.
- Search with no matches: muted "no matches for '<query>'" line; preview clears via the existing nil path.
- Single repo in Group-by-repo: still renders the one header.
- A workspace that becomes pending while in Pinned-pending mode pulses into the pinned section; clearing the prompt pulses it back.

## 6. Data model changes

`session.Instance` (+ `session/storage.go` `InstanceData`):

- Add `LastActivityAt time.Time` — set from the metadata tick's batch timestamp when `updated == true` (subject to anti-thrash). Serialized in `InstanceData` (back-compat: missing → zero → fall back to `UpdatedAt`/`CreatedAt`).
- Add waiting-for-user tracking: `IsWaitingForUser() bool` plus the internal flag/derivation in §7.5. Recompute-only (not persisted) in v1; first-paint shows non-pending until the first tick (acceptable).
- Keep `HasUpdated()` as-is; introduce the richer semantics behind new accessors so the metadata branch logic is unchanged (R8).

`config` (UI state) — extend the **`AppState` interface** (not just the `State` struct) with `GetSidebarMode() SidebarMode` / `SetSidebarMode(SidebarMode)`, a JSON field on `State`, validation of unknown values → `ModeManual`, and `state.json` back-compat. Search text is **not** persisted.

## 7. Architecture & implementation

### 7.1 View-model layer (standalone, stateless) (R9)

Introduce package-level pure builders (own file, e.g. `ui/sidebar_view.go`), independent of `List`'s mutable state:

```go
type SidebarMode int
const ( ModeManual SidebarMode = iota; ModeGroupByRepo; ModeRecentActivity; ModePinnedPending )

type rowKind int
const ( rowHeader rowKind = iota; rowInstance )

type displayRow struct {
    kind     rowKind
    header   string            // when kind == rowHeader
    instance *session.Instance // when kind == rowInstance
    number   int               // 1-based, continuous over visible instances
}

// Pure: no timers, no I/O, deterministic. Trivially unit-testable.
func buildView(items []*session.Instance, mode SidebarMode, filter string) []displayRow
```

`List` shrinks to: canonical `items`, `selected *session.Instance`, `mode`, `filter`, a cached `[]displayRow` (recomputed on change), and delegation to `buildView` + the animator. `items` remains what `GetInstances()` returns and what is persisted, so save/load is unaffected.

### 7.2 Selection by identity (R3, R6)

Replace index-based selection with **identity-based**, and make destructive operations target an explicit instance:

- Store `selected *session.Instance`; derive a visible index only at render time.
- New/`clarified` `List` API:
  - `GetSelectedInstance() *session.Instance` — the **visible** selection (nil if hidden/none).
  - `SelectInstance(inst *session.Instance)` — select a visible instance by identity.
  - `SelectNewInstance(inst *session.Instance)` — select a just-created, possibly-unstarted/hidden instance, guaranteeing it is presented/visible while naming (see §7.8 new-instance handling).
  - `RemoveInstance(inst *session.Instance)` — remove an **exact** instance (used by cancel/start-failure).
  - `KillSelected()` — kill the currently selected visible instance (replaces today's index-based `Kill()`).
  - `MoveSelectedUp()/MoveSelectedDown()` — canonical reorder, **Manual mode only**.
  - `VisibleCount() int` / `HasVisible() bool` — visible counts.
  - `NumInstances() int` — **canonical** count (unchanged meaning).
- `Up()/Down()` move among **visible instance rows**, skipping headers, wrapping as today.
- After any recompute: if `selected` is still visible keep it; if hidden by filter, retain it as the desired selection while `GetSelectedInstance()` reports nil (so preview empties); if removed, clamp to the nearest visible instance.

**Canonical-vs-visible call-site map (must all be updated/verified):** `GlobalInstanceLimit` checks (`app.go` ~635, ~664) → canonical `NumInstances()`; "empty list" guard (~802) and selection guards (~806) → visible; `snapshotActiveInstances` (~951) → canonical; `Attach()` → selected identity, not `items[idx]`; kill confirmation action and `cancelPromptOverlay` → `RemoveInstance(exact)` / `KillSelected()`; `instanceStartedMsg` failure path → `RemoveInstance(msg.instance)`; new-instance flows (~656, ~677) → `SelectNewInstance(instance)`.

### 7.3 Sorting & grouping (R7)

Pure helpers over a snapshot of `items` returning `[]displayRow`: `buildManual`, `buildGroupByRepo`, `buildRecentActivity`, `buildPinnedPending`. Repo identity = **repo root path** (worktree repo path), label = `RepoName()`; unknown/unstarted → a "(no repo)" bucket; duplicate basenames disambiguated by path. Stable sorts; deterministic tiebreaks (`CreatedAt`, then `Title`). The existing `repos map[string]int` is repurposed/replaced by a path-keyed structure (or kept only for the legacy multi-repo display rule outside group mode).

### 7.4 Filtering (R6)

`matches(inst, query)` = case-insensitive substring of `query` in `Title` OR repo path. Applied before grouping/sorting; empty groups and zero-child headers removed. Selection handling per §7.2.

### 7.5 Pending detection & last-activity (R1, R2, R8)

In `app.go` `metadataUpdateDoneMsg`, **preserve the existing branch structure** and add orthogonal side-effects (do not collapse it):

```go
batchNow := time.Now() // one timestamp for the whole result batch
for _, r := range msg.results {
    if r.instance.Status == session.Paused { continue }   // unchanged
    if r.updated {
        r.instance.SetStatus(session.Running)             // unchanged
        r.instance.NoteActivity(batchNow)                 // NEW: anti-thrash-aware LastActivityAt
    } else if r.hasPrompt {
        r.instance.TapEnter()                             // unchanged (AutoYes only)
    } else {
        r.instance.SetStatus(session.Ready)               // unchanged
    }
    r.instance.RefreshWaitingForUser(r.hasPrompt)         // NEW: derives IsWaitingForUser(), AutoYes-aware
    // ... existing diff-stat handling unchanged ...
}
// recompute view once; trigger pulse/crossfade if positions changed
```

`NoteActivity` applies the anti-thrash dwell so streaming `Running` agents don't reorder every tick. `RefreshWaitingForUser` clears pending after a successful AutoYes/host-side approval and never pins Paused/Loading.

### 7.6 Rendering (sections, numbering)

`InstanceRenderer.Render` continues to render a single workspace row; `List.String()` interleaves header rows (new header style), forces the repo label off inside group mode (header already shows it), and applies pulse/crossfade decoration from the animator. Numbering is continuous over visible instances. Title shows the active mode and, when filtering, the query + match count.

### 7.7 Animation engine (R4)

- A standalone `animator` type tracks, per instance, prior vs. current visible slot and active pulse timers, exposing `Step()` (advance one frame) and `Frame()` (current decorations). No wall-clock inside; frames are advanced by `animTickMsg`.
- `app.go` adds `animTickMsg` with **generation IDs** and an `animating` flag on `home`. Scheduling rules per §5.4 (single loop, stale-tick no-op, retarget-on-change, batched Cmd). Independent of the 500ms metadata and 100ms preview tickers.
- On each view recompute, diff old vs. new visible slots; decorate changed rows with a pulse (and crossfade for bulk changes). Selection/order are already committed; the animator only affects rendered decoration. Reduced-motion predicate bypasses it entirely.

### 7.8 Search input handling & app `state` (R5, R6)

- Add `stateSearch` to `app/app.go`'s `state` enum. Entered by `/`, exited by `Esc`/`Enter`.
- Add `stateSearch` to `handleMenuHighlighting`'s suppression set (alongside prompt/help/confirm) so query letters aren't intercepted as menu keys.
- Add `StateSearch` to `Menu.SetInstance`'s special-state guard so background `instanceChanged()`/metadata ticks don't reset the search menu.
- Key routing while searching per §5.2 (arrows navigate; letters are text; `Esc` restores `preSearchSelection`; `Enter` commits). Define `Esc` precedence: in `stateSearch`, `Esc` handles search first (not preview/terminal scroll).
- **New-instance handling:** entering `stateNew`/`statePrompt` **suspends the active filter** and presents the list so the new (unstarted, `RepoName()`-erroring, empty-title) row is **visible and selected** via `SelectNewInstance`. On cancel/Ctrl+C/start-failure, the exact instance is removed via `RemoveInstance(inst)` and the prior filter/selection is restored.

### 7.9 Persistence (R8)

- Extend the `AppState` interface + `State` struct with `SidebarMode` (validated; unknown → `ModeManual`); save on change, load in `newHome`. Accept the existing full-`state.json`-write-per-change cost.
- Add `InstanceData.LastActivityAt` (JSON, back-compat default zero).
- Canonical `items` order persists exactly as today via `SaveInstances`, independent of display mode.

### 7.10 Affected files

- `ui/list.go` — shrink to canonical items + identity selection + delegation; header rendering; animator hookup.
- `ui/sidebar_view.go` *(new)* — `SidebarMode`, `displayRow`, pure `buildView` + builders.
- `ui/animator.go` *(new)* — standalone, timer-free animator with `Step()/Frame()`.
- `ui/list_test.go`, `ui/sidebar_view_test.go`, `ui/animator_test.go` — tests.
- `ui/menu.go` — menu entries + `StateSearch` (incl. `SetInstance` guard).
- `app/app.go` — `stateSearch`, key routing, mode cycle, search input + `preSearchSelection`, anim tick + generation/flag, `LastActivityAt`/pending wiring, identity call-site updates, recompute calls.
- `keys/keys.go` — `s`/`S`/`/` (and search-mode handling).
- `session/instance.go` — `LastActivityAt`, `NoteActivity`, `IsWaitingForUser`/`RefreshWaitingForUser`.
- `session/storage.go` — serialize `LastActivityAt`.
- `config/state.go` — `AppState` interface + `State` `SidebarMode` + validation.
- `app/help.go` — help text.

## 8. Acceptance criteria

1. A single key cycles the sidebar Manual → Group-by-repo → Recent-activity → Pinned-pending (and a second key reverses); the active mode is visible and **persists across restarts**; an unknown persisted value falls back to Manual.
2. **Group by repo:** each workspace appears under exactly one repo header keyed by repo **path**; groups are alphabetical; a single-repo setup still renders its header; two repos sharing a basename get disambiguated labels.
3. **Recent activity:** the workspace with the most recent observed change is first; **no thrash** — concurrently streaming agents do not swap positions every tick, and the selected/`Running` row is not reordered out from under the user.
4. **Pinned-pending:** every workspace for which `IsWaitingForUser()` is true appears in a top "Pending" section in canonical order; others appear below in canonical order; a prompt resolved by AutoYes (incl. Windows host-side) is **not** pinned; answering/clearing a prompt moves the row back down.
5. **Search:** `/` opens search; typing filters live by title or repo path (case-insensitive substring); letters including `j`/`k` are text, only arrows navigate; non-matches hide; empty groups disappear; `Enter` keeps the filter, `Esc` clears it **and restores the pre-search selection**; search works in every mode.
6. **Selection integrity:** across every mode switch, sort change, and filter apply/clear, the selected workspace remains the **same** workspace (restored after `Esc`); when its row is hidden, `GetSelectedInstance()` is nil and the preview shows the empty state; selection never silently lands on an unrelated workspace.
7. **Identity operations:** cancelling new-instance creation, a failed start, and kill each remove the **exact** target instance even if the mode/filter changed meanwhile; attach targets the selected identity; creating a new workspace while a filter is active shows and selects the new workspace.
8. **Manual reorder** still works with `K`/`J` in Manual mode and persists; in other modes it is a no-op with a visible hint (never an auto-switch).
9. **Motion:** reordering/sorting/filtering/pinning visibly emphasizes the affected sidebar rows (pulse/crossfade); animations never corrupt selection, ordering, or persistence; a rapid second change mid-animation retargets without spawning duplicate tick loops or stale frames; reduced-motion/instant mode is used automatically below the size/count threshold.
10. No regression to create/kill/pause/resume/attach/push, to the preview/diff/terminal panes (incl. the nil-selection empty state), or to existing persisted state (old `state.json`/instances load with sane defaults).
11. `go build ./...` and `go test ./...` pass on Windows and Unix.

## 9. Test plan

Match the existing table/unit style in `ui/list_test.go` (`session.NewInstance`, `testify/require`). View-model, sorting, and animator logic are **pure/deterministic** — tested without timers or tmux.

**Ordering/grouping (`sidebar_view_test.go`):**
- Manual order equals `GetInstances()`.
- Group-by-repo: correct path-keyed buckets, alphabetical groups, header count, single-repo header present, duplicate-basename disambiguation.
- Recent-activity: order by `LastActivityAt` desc + tiebreaks; updating one instance re-sorts it to top.
- **Recent-activity anti-thrash:** updating `LastActivityAt` on several instances within one tick batch (and across rapid ticks) preserves stable order; a selected/`Running` row is not reordered (R2).
- Pinned-pending: pending subset on top in canonical order, remainder below; toggling pending re-partitions; **Paused/Loading with `hasPrompt` are NOT pinned**; **AutoYes-resolved prompt is NOT pinned** (R1).

**Filtering & selection (`list_test.go`):**
- Title and repo-path matches; case-insensitivity; no-match → empty visible set + empty state; empty groups removed; filter composed with each mode (incl. mode switch with an active filter).
- **Search key routing:** typing `j`/`k` appends to the query and does **not** navigate; arrows do navigate (R5).
- **Pre-search restore:** filter out the selected item, then `Esc` → `GetSelectedInstance()` is the original; `Enter` keeps the new visible selection (R6).
- Selected hidden → `GetSelectedInstance()` nil and preview empty; `Up()/Down()` skip headers and wrap in sectioned layouts.

**Identity operations (`list_test.go` + `app_test.go`):**
- New workspace while a filter is active is shown+selected; cancelling `stateNew`/prompt-after-name removes the **exact** unstarted instance; start-failure removes the **exact** failed instance even if mode/filter changed; kill/attach target the right identity (R3).

**Rendering:**
- Continuous numbering over visible instances; headers non-selectable; mode label + match count shown; existing truncation/diff-stat rendering unaffected.
- **Nil-selection regression:** `instanceChanged()` with zero visible matches drives the existing nil fallbacks in preview/diff/terminal without panic.

**Animator (`animator_test.go`):**
- Pulse/crossfade selected correctly; settles to the final layout; **mid-animation retarget** yields the correct final state; **stale-generation ticks no-op**; reduced-motion path is instant. Frames advanced via injected `Step()`, no sleeps (R4).

**Session/storage & config:**
- `LastActivityAt` round-trips; missing field → zero → fallback; `IsWaitingForUser`/`RefreshWaitingForUser` behavior incl. AutoYes on/off and Windows host-side.
- `metadataUpdateDoneMsg` keeps the exact `Running`/`Ready` transitions while adding activity/pending side-effects (R8).
- `SidebarMode` persists/loads via `AppState`; **old `state.json`** and **invalid mode value** fall back to Manual.
- Existing `app_test.go` flows still pass.

## 10. Edge cases

- Unstarted/no-worktree workspaces (`RepoName()` error) → "(no repo)" bucket; visible+selected while naming; never crash.
- Duplicate repo basenames across paths → distinct buckets, disambiguated labels (R7).
- Paused/Loading: excluded from pending; sortable by activity; grouped normally.
- Pending pin + active search simultaneously: pinned section is still filtered.
- Rapid activity churn: anti-thrash + reduced-motion prevent flicker/animation thrash (R2, R4).
- Selected workspace killed (possibly mid-animation): selection clamps predictably; animator drops the missing instance without an out-of-bounds (test required).
- Mode switch mid-animation: retarget to the new layout, single loop (R4).
- Very small terminal / many workspaces: motion auto-disables; layout still correct.
- Continuous numbering agrees with any number-key selection that exists.

## 11. Open questions (remaining after review)

1. **Anti-thrash thresholds** — exact dwell (ticks/seconds) and whether to also suppress reorder while `Running`; resolve with real multi-agent usage in Phase 3.
2. **Backend activity signal** — keep the `HasUpdated()` screen-hash heuristic for v1, or invest in a backend-maintained agent-activity timestamp (tmux/conpty) for trustworthy Recent-activity? (Long-term recommended.)
3. **Persist pending across restart** — recompute-only (chosen for v1) vs. persisted for first-paint stability.
4. **Slide animation** — ship pulse/crossfade only (v1), or schedule true multi-line sliding with a virtual-canvas spec later?
5. **Reduced-motion control** — config flag, auto-thresholds, or both, and the default threshold values.

## 12. Phased delivery / milestones

1. **Data & signals** — `LastActivityAt` + `NoteActivity` (anti-thrash), `IsWaitingForUser`/`RefreshWaitingForUser` (AutoYes-aware), metadata-loop wiring preserving existing transitions, storage round-trip (+ tests). Resolve the pending-signal definition here.
2. **View-model + identity selection** — extract `buildView`/builders and the identity-selection `List` API; render Manual mode from `displayRow` with **behavior identical to today**; add unit tests that inject synthetic multi-section/filtered `[]displayRow` so the clamp/skip/identity logic is covered **in this phase** (don't wait for Phase 3/4).
3. **Modes** — group-by-repo (path-keyed), recent-activity (with the anti-thrash rule resolved **here**, not deferred), pinned-pending, headers, `s`/`S` cycle, persistence (+ tests).
4. **Search** — `stateSearch`, key routing (arrows-only nav), `preSearchSelection` restore, filtering, menu/help, new-instance filter suspension, empty states (+ tests).
5. **Motion** — standalone animator, pulse/crossfade, the **tick-scheduling guard (`animating` flag + generation ID) as an explicit acceptance item**, reduced-motion predicate (+ tests).
6. **Polish & docs** — help text, menu copy, full regression pass, README/docs note.
