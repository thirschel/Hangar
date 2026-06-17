# Feature: "Hangar" — GitHub Pages Marketing Site

> **Audience:** maintainers and AI agents implementing the public marketing page for this fork
> (`thirschel/Hangar`). This is a **feature specification**, not an implementation. It defines
> what to build, how we'll know it's done (acceptance criteria), what to test, and a general
> implementation outline.
>
> **Status:** Reviewed by three models (GPT-5.5, Gemini 3.1 Pro, Claude Opus 4.8) and revised — see
> Appendix B. Rebranded to **"Hangar"** for repo **`thirschel/Hangar`**. **Scope:** marketing-page
> branding and repo/Page URLs only; the Go module (`hangar`) and `cs` binary are **not** renamed
> in this feature (see Appendix A).

---

## TL;DR

Rebrand and overhaul the **existing** Next.js marketing site in `web/` into a small, polished
**"Hangar"** landing page deployed to GitHub Pages. The page showcases the app's features —
**led by this fork's native-Windows support** — and gives basic start/install instructions.
"Hangar" is the page brand with a working tagline like *"a hangar for all your copilots."*
The site already exists and already deploys via `.github/workflows/deploy-pages.yml`; this feature
is a **content + branding overhaul**, not a greenfield build.

---

## 1. Problem & motivation

This fork's headline differentiator is **native Windows support** (no WSL, no tmux). But the only
public-facing surface — the marketing site in `web/` — does not reflect that:

- It is branded **"Hangar"** and links to upstream **`smtg-ai/claude-squad`** everywhere
  (`web/src/app/page.tsx`, `web/src/app/layout.tsx`).
- Its install section shows **only Unix paths** (Homebrew, `install.sh`) and lists prerequisites as
  "tmux, gh" — both wrong/misleading for the native-Windows audience this fork targets.
- It never mentions the fork's actual features: the **session host**, **TUI-restart persistence**,
  **attach/detach**, **host-side AutoYes**, or the **ConPTY + VT-emulator** architecture.

A visitor arriving from a Windows-native context currently sees a page that tells them to install
tmux. We need a landing page that sells *this* project to *its* audience.

## 2. Goals & non-goals

**Goals**
- A single-page marketing site branded **"Hangar"** with a memorable tagline.
- Clearly showcase the core features, with **native Windows** as the lead story.
- Provide **basic start instructions** that work for the native-Windows build first, with Unix
  install paths secondary.
- Point links/metadata at this fork (`thirschel/Hangar`) where appropriate, preserving the
  upstream attribution and the upstream Unix installers (the fork ships no releases — see §10).
- Reuse the existing `web/` Next.js app, static export, and Pages deploy workflow.

**Non-goals (this feature)**
- Deep code rename: the Go module path and internal package/string references are now `hangar`; the `cs` binary and upstream install scripts stay unchanged (covered by the rename contract).
- Multi-page docs site, blog, or CMS.
- Backend/server features; the site stays a **static export** (`output: "export"`).
- Custom domain setup (can be a follow-up).

## 3. Brand

| Aspect | Decision |
|--------|----------|
| **Name** | **Hangar** (brand and repo name; Go module/binary are `hangar` / `cs`). |
| **Tagline (working)** | *"A hangar for all your copilots."* |
| **Tagline alternatives** | "Where your copilots land." · "A home base for every AI copilot." · "One hangar for every AI agent." |
| **Voice** | Developer-direct, concise, slightly playful; same tone as the README. |
| **Applied in** | Page `<h1>`, `layout.tsx` `metadata` (title/description/keywords), OpenGraph + Twitter cards, footer, browser tab title, and (optionally) a small logo/wordmark. |


## 4. Feature showcase (content the page must convey)

The page must present these, **in priority order**, sourced from `README.md` and
`docs/native-windows.md`:

1. **Runs natively on Windows** — no WSL, no tmux. A background **session host** owns a real
   Windows console (ConPTY) per agent; the `cs` TUI talks to it over a named pipe and renders via a
   VT emulator. *(This is the lead headline.)*
2. **Supervise multiple AI agents at once** — Claude Code, Codex, Gemini, Copilot CLI, Aider, in one
   terminal UI.
3. **Isolated git worktrees** — every session works on its own branch; no cross-task conflicts.
4. **Review before you ship** — diff view; commit/push or checkout/pause per session.
5. **Background + AutoYes** — agents keep working (and auto-accept prompts) even while the TUI is
   closed; AutoYes pauses while you're attached.
6. **Attach / detach + persistence** — `Enter` to attach, `Ctrl+q` to detach; sessions survive
   **TUI restarts** (reattached via the session host).

Each item needs a one-line benefit headline plus ≤2 lines of supporting copy. The existing demo
video block is retained (or replaced with a Windows-specific capture — see Open Questions).

## 5. Start instructions (content the page must convey)

Present **native Windows first**, then Unix, mirroring `README.md`:

- **Native Windows (this fork):**
  ```bat
  :: Requires Go 1.25+ and git
  git clone https://github.com/thirschel/Hangar.git
  cd Hangar
  build.bat
  ```
  Then put `cs.exe` on `PATH`, run `cs` from inside a git repo. The agent (e.g. GitHub Copilot CLI)
  must be installed and resolvable (`where copilot`).
- **Unix / macOS (upstream):** Homebrew (`brew install claude-squad` **plus the `cs` symlink** from
  the README) and the `install.sh` one-liner, clearly labeled as the tmux-based path. **These
  intentionally reference upstream `smtg-ai/claude-squad`** — the fork publishes no release artifacts
  of its own, so `install.sh` and the Homebrew formula install the upstream binary (see §10).
- **Prerequisites**, correctly scoped: `gh` (all platforms); **tmux only on Unix/macOS/WSL** — *not*
  for the native Windows build.

> **Messaging note:** the Unix install yields the **upstream** binary, which does not include this
> fork's native-Windows session-host features (those are Windows-only anyway). Phrase the Unix
> section so the page doesn't over-promise fork-specific behavior to Unix installers.

Copy/paste blocks reuse the existing `CopyButton` component.

## 6. Information architecture (page sections)

Single scrolling page, in order:
1. **Header** — "Hangar" wordmark; links: GitHub (this fork), Docs (README), theme toggle.
2. **Hero** — name + tagline + 1-sentence positioning ("Native-Windows-first multi-agent manager").
3. **Demo** — video/screenshot.
4. **Feature showcase** — the six items from §4.
5. **Start instructions** — the blocks from §5.
6. **Footer** — AGPL-3.0 license link + upstream attribution to `smtg-ai/claude-squad`.

## 7. Acceptance criteria

A reviewer can check each of these as pass/fail:

**Branding & content**
- [ ] The page `<h1>`, browser tab title, and `metadata` all read **"Hangar"** (no visible
      "Hangar" except where attributing upstream).
- [ ] A tagline is present in the hero.
- [ ] Native-Windows support is the **first** feature shown and is described accurately
      (session host, no WSL/tmux).
- [ ] All six features from §4 are present, each with a headline + supporting line.
- [ ] Start instructions show the **native Windows `build.bat`** flow first; Unix install second.
- [ ] Prerequisites correctly state tmux is **Unix-only**.

**Links & metadata**
- [ ] All GitHub/repo/raw links point at **`thirschel/Hangar`** (not `smtg-ai`), **except**
      (a) the footer upstream attribution and (b) the upstream Unix install commands
      (`install.sh` raw URL and `brew install claude-squad`), which intentionally reference
      `smtg-ai` because the fork ships no releases of its own (see §10).
- [ ] `layout.tsx` `metadata` (title, description, keywords) reflects "Hangar".
- [ ] OpenGraph/Twitter **canonical URL** uses the published site URL
      **`https://thirschel.github.io/Hangar/`**; repository links use
      `https://github.com/thirschel/Hangar`. `metadataBase` is set to the site URL so relative
      OG image paths resolve absolutely (avoids the Next `metadataBase` build warning).
- [ ] A Hangar-branded **OpenGraph/Twitter image** exists (e.g. `web/public/og-hangar.png`,
      1200×630) and is referenced in `metadata.openGraph.images` and `metadata.twitter.images`
      (the card is `summary_large_image`, which renders broken without an image).
- [ ] Any remaining use of the literal `Hangar` is limited to technical install/repo/binary
      context or upstream attribution (not user-facing branding).
- [ ] No broken internal links; external links use `target="_blank" rel="noopener noreferrer"`.

**Build & deploy**
- [ ] `cd web && npm run build` produces a static export in `web/out` with **no errors or warnings**
      (treat a missing `metadataBase` or image-optimization warning as a failure).
- [ ] `cd web && npm run lint` passes with **no new errors** (note: lint is **not** in CI today — see
      §8/§9 for wiring it into the workflow).
- [ ] The `Deploy to GitHub Pages` workflow succeeds on PR (build job) and on push to `main`
      (build + deploy jobs).
- [ ] `next.config.ts` `basePath` stays **`/Hangar`** to match the renamed repo (see §9
      and Appendix A). The built `web/out` is verified **served under `/Hangar/`**: every asset
      and internal link resolves at that base path (no 404s for `/Hangar/_next/...`).
- [ ] If any `next/image` is introduced, `next.config.ts` sets `images.unoptimized = true` (the
      default optimizer is incompatible with `output: "export"` and will fail the build).

**UX & quality**
- [ ] Layout is responsive with **no horizontal scroll at ~375px** and is usable at desktop widths.
- [ ] Light **and** dark themes render correctly (existing `ThemeToggle`).
- [ ] Keyboard focus is visible for header links, the theme toggle, and copy buttons.
- [ ] The autoplay/loop demo `<video>` respects `prefers-reduced-motion` (or exposes `controls`,
      which it currently does).
- [ ] Lighthouse (mobile) ≥ **90 Performance**, ≥ **95 Accessibility**, ≥ **90 SEO**; no critical
      a11y violations (contrast, alt text, `aria-label`s, visible focus).
- [ ] The export produces a `404.html`; it is either Hangar-branded (`not-found.tsx`) or an
      explicitly accepted default (see §10).

## 8. Tests needed

There is **no test framework in `web/` today** (only `next build` and `next lint`, and CI currently
runs **only `npm ci` + `npm run build`** — `deploy-pages.yml:36-45`). The page is a static export, so
"tests" are primarily build/lint/link/manual gates. **The checks below are required gates and §9
wires them into `deploy-pages.yml` (not optional)** — §8 and §9 must stay consistent.

**Required CI gates**
1. **Build gate** — `npm run build` succeeds and is **warning-clean** (fail on missing
   `metadataBase` / image-optimization warnings). Already run by `deploy-pages.yml`.
2. **Lint gate** — add `npm run lint` as a CI step (not present today) so the lint AC is enforced.
3. **`basePath`-aware link check** — serve the built `out/` at
   `http://localhost:PORT/Hangar/` and run `linkinator`/`lychee`, asserting 2xx/3xx for
   internal links **and assets** (`/Hangar/_next/...`) and for in-page **hash anchors**
   (`#features`, `#install`). Serving `out/` at the server root instead produces false 404s.
4. **Brand/leak check** — grep built `out/**/*.html`: assert **"Hangar"** appears in `<title>` and
   `<h1>`, and that **no `smtg-ai`** reference remains **except** the footer attribution and the
   upstream Unix install commands (`install.sh` URL, `brew install claude-squad`).
5. **OG/meta assertion** — parse `out/index.html`: `og:image`/`twitter:image` resolve to a 200 asset
   (under the base path) and the card is `summary_large_image`; `title`/`description` are the Hangar
   values.
6. **HTML validation** — run a static validator (e.g. `html-validate`) against `out/index.html` for
   semantic/accessibility correctness.

**Recommended (optional, low cost)**
7. **Smoke test (Playwright)** — load the exported page **served under `/Hangar/`** and assert:
   `<h1>` = "Hangar"; a native-Windows feature heading is visible; install code blocks present; theme
   toggle flips `data-theme`. The **`CopyButton` clipboard** assertion needs a secure context +
   clipboard permissions in headless CI — gate it or grant `clipboard-read/write`.
8. **Responsive/visual spot-check** — manual at 375px and 1280px, light + dark.

**Out of scope:** unit tests for individual React components; full visual-regression suite.

## 9. General implementation outline

Files in `web/` to change (no new app scaffolding required):

| File | Change |
|------|--------|
| `web/src/app/page.tsx` | Replace headline/tagline; rewrite feature list into the six items (§4); reorder install blocks to Windows-first (§5); fix prerequisites; repoint links to `thirschel/Hangar` **except** the upstream Unix install commands and footer attribution (§7). |
| `web/src/app/layout.tsx` | Update `metadata` title/description/keywords to "Hangar"; set OpenGraph/Twitter to the **site** URL `https://thirschel.github.io/Hangar/`; set `metadataBase` to that URL; add `openGraph.images`/`twitter.images` pointing at the OG image. |
| `web/src/app/globals.css` | Update base/accent color variables for the rebrand if needed (theme colors live here too, not only in `page.module.css`). |
| `web/src/app/page.module.css` | Add styles for new feature-grid / install sections as needed; keep theme variables. |
| `web/src/app/components/*` | Reuse `CopyButton` and `ThemeToggle` as-is; add new copy snippets (e.g. the `build.bat` block). Optional: add a tiny pre-hydration inline script to avoid the light-theme FOUC (`ThemeToggle` sets `data-theme` in `useEffect`, post-hydration). |
| `web/src/app/favicon.ico` | The App-Router favicon lives **here**, not in `web/public/`. Replace if rebranding the icon. |
| `web/public/` | Add the Hangar OG image (e.g. `og-hangar.png`, 1200×630) and any wordmark. **Asset landmine:** assets referenced via plain `<img src="/…">` must include the `basePath` (`/Hangar/og-hangar.png`) or they 404 on Pages. |
| `web/next.config.ts` | **Keep** `basePath: "/Hangar"` for production (matching the renamed repo and Pages path). Add `images: { unoptimized: true }` **iff** any `next/image` is introduced (required for `output: "export"`). |
| `.github/workflows/deploy-pages.yml` | **Add** the §8 required gates as CI steps: `npm run lint`, the `basePath`-aware link check, and the brand/leak + OG/meta checks (the current no-op "Update Next.js config for static export" step at lines 39-42 can be replaced). Build/deploy jobs otherwise reused. |

**Deployment prerequisites (fork):**
- In the fork's repo settings, **Pages → Source = GitHub Actions** must be enabled.
- Published URL will be **`https://thirschel.github.io/Hangar/`** (project-pages path matches
  `basePath`). Update the README's site link accordingly.

**Implementation notes**
- Keep everything client/static — no server components requiring a runtime, since `output: "export"`.
- Preserve dark-mode behavior and `localStorage` theme persistence.
- Demo asset: the current `<video src=...github user-attachments...>` is an upstream asset; either
  keep it or swap for a native-Windows capture (Open Questions).

## 10. Risks & open questions

- **Does the fork publish its own releases?** Today it does not — `install.sh` and the Homebrew
  formula fetch upstream `smtg-ai/claude-squad` artifacts, and the native-Windows path is
  **source-build only** (`build.bat`). This is why the Unix install links can't point at the fork.
  Decide: ship fork releases (then revisit those links), or stay source-build-on-Windows. **Until
  decided, the page must not imply a fork-published Unix/Homebrew binary.**
- **Demo media:** keep the upstream video, or record a Windows-native demo? (Recommend a short
  Windows capture for authenticity; not blocking.) The autoplay/loop `<video>` is the page's main
  performance weight — factor it into the Lighthouse perf budget.
- **Analytics:** none in this pass (explicit decision) unless requested.
- **Deep code rename:** repo and Pages paths now use Hangar, but the Go module, `cs` binary, and
  installer/release plumbing still use `Hangar`; defer that larger rename to Appendix A.
- **Tagline:** final wording TBD; candidates in §3.
- **404 page:** brand a `not-found.tsx`, or accept the default Next 404? (Default is acceptable for a
  first pass.)
- **Upstream attribution:** confirm footer wording satisfies AGPL-3.0 attribution expectations.

## Appendix A — Future work: deep code rename (out of scope here)

The repo and public brand are now **Hangar** (`thirschel/Hangar`, Pages `basePath` `/Hangar`).
Deferred distribution work is limited to installer/release plumbing; Go module path `hangar` and the
`cs` binary name, install scripts (`install.sh`, `install.ps1`, `install.bat`, Homebrew formula),
in-code/string references, and release config (`.goreleaser.yaml`). The upstream Unix installer
references stay `smtg-ai/claude-squad` until this fork publishes its own releases.

## Appendix B — Multi-model review summary

This spec was reviewed in parallel by three models (GPT-5.5, Gemini 3.1 Pro, Claude Opus 4.8). All
three independently **verified the doc's codebase claims as accurate** and returned a **"minor
revisions"** verdict. Convergent and notable findings, and how each was addressed:

| # | Finding | Raised by | Resolution |
|---|---------|-----------|------------|
| 1 | CI/test gates were "required" in §8 but "optional" in §9 (contradiction). | GPT-5.5, Gemini, Opus | §8 + §9 now agree: lint, `basePath`-aware link check, and brand/OG checks are **required CI steps** added to `deploy-pages.yml`. |
| 2 | `basePath` landmine — serving `out/` at root, or `/`-rooted assets, 404s on Pages. | Gemini, Opus | Added AC + test step requiring `out/` served under `/Hangar/`; added `public/` asset-prefix note in §9. |
| 3 | `next/image` breaks `output: "export"` builds. | Gemini, Opus | §7 AC + §9 note: set `images.unoptimized = true` if `next/image` is used; otherwise use `<img>`. |
| 4 | "Repoint **all** links to `thirschel`" AC contradicts the Unix installer, which **must** stay upstream (fork ships no releases). | Opus | §2, §5, §7, §10 now carve out the upstream `install.sh`/Homebrew commands + footer attribution; added an "own releases?" open question. |
| 5 | Broken social card — `summary_large_image` declared with no OG image. | Opus | OG image is now a deliverable with an AC + OG/meta test; `metadataBase` required. |
| 6 | Metadata URL ambiguity (repo URL vs published site URL). | GPT-5.5, Opus | §7 AC splits canonical/OG/Twitter (site URL) from repo links (repo URL). |
| 7 | `favicon.ico` is at `web/src/app/favicon.ico`, not `web/public/`; `globals.css` also holds theme colors. | GPT-5.5, Opus | §9 file table corrected; `globals.css` row added. |
| 8 | Missing SEO/perf Lighthouse thresholds, 404 page, reduced-motion, focus visibility, hash-anchor + HTML-validation tests. | Gemini, Opus | Added to §7 ACs and §8 tests; 404 + analytics + perf added to §10. |

Minor non-blocking suggestions also incorporated: theme-FOUC pre-hydration script (optional, §9), the
clipboard secure-context caveat for the Playwright smoke test (§8), the Homebrew `cs` symlink (§5),
and a Unix "upstream binary" messaging note (§5).
