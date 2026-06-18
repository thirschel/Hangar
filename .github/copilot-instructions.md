# Hangar — Copilot instructions

Starting context for AI agents working in this repository. Read this first, then dive into
the linked docs for anything you change deeply.

## What Hangar is

**Hangar** is a native-Windows-first manager for AI coding agents (Claude Code, Codex,
Gemini, GitHub Copilot CLI, Aider, …). It runs each agent in an isolated git worktree and
lets you review work before it ships. It is a fork of
[`claude-squad`](https://github.com/smtg-ai/claude-squad) and is licensed **AGPL-3.0**.

- **Go module:** `hangar` &nbsp;·&nbsp; **binary:** `cs` &nbsp;·&nbsp; **state dir:** `~/.hangar/`
  (`config.json`, `state.json`, `host.json`, `daemon.pid`).
- **Go version:** 1.25 (`go.mod`).
- The `cs` engine is usable standalone (TUI), and also powers the desktop app under the hood.

## The three surfaces

| Surface | Location | Stack | Role |
| --- | --- | --- | --- |
| **Core daemon/CLI (`cs`)** | repo root | Go 1.25, Charmbracelet (bubbletea/bubbles/lipgloss), cobra | TUI + session host engine |
| **Desktop app** | `desktop/` | Electron + TypeScript + React + Vite | Thin client over the `cs` daemon |
| **Marketing site** | `web/` | Next.js 15 / React 19 (static export) | Public website — *not* an app UI |

## Go package map (core engine)

- `main.go` — cobra root + subcommands: `debug`, `version`, `reset`, hidden `session-host`,
  hidden `--daemon`.
- `app/` — bubbletea TUI app (`app.go`; `attach_windows.go` / `attach_other.go`; `help.go`).
- `ui/` — TUI components (`list`, `menu`, `preview`, `diff`, `sidebar_view`,
  `sessionBrowser`, `tabbed_window`, `animator`, `overlay/`, `safe_display`).
- `session/` — the core. `instance.go` defines the `Instance` domain type and `Status`
  (`Running`/`Ready`/`Loading`/`Paused`); `storage.go`; `terminal.go` (+ `_unix`/`_windows`).
  Subpackages:
  - `session/git/` — git worktree lifecycle, diffs, path-safety.
  - `session/tmux/` — tmux backend (Unix/macOS/WSL).
  - `session/winhost/` — native Windows session host (ConPTY + VT emulator); `proto/` = pipe protocol.
  - `session/copilot/` — discovery/indexing/browsing of local Copilot CLI sessions.
  - `session/agentcmd/`, `session/promptpolicy/`.
- `cmd/` — external command execution helpers.
- `config/` — config + state (read/written under `~/.hangar/`).
- `daemon/` — background AutoYes daemon (`_unix`/`_windows`).
- `keys/` — keybinding definitions. `log/` — structured logging.

## Architecture you must respect

- **Cross-platform seam:** terminal sessions go through the `session.TerminalSession`
  interface — **tmux** on Unix/macOS/WSL, a **native Windows session host** on Windows.
  Any change to attach/AutoYes/persistence must keep the **Unix/tmux path** working.
- **Native Windows model:** a detached `cs session-host` process owns one **ConPTY** console
  per agent and renders it through a VT emulator (`charmbracelet/x/vt`); the TUI and desktop
  app are thin **named-pipe JSON-RPC** clients (length-prefixed; **protocol v3**). Because the
  consoles live in the host, sessions survive TUI/app restarts — but **not** a reboot or
  `cs reset`. Authoritative design: **[`docs/native-windows.md`](../docs/native-windows.md)**.

## Build, test, lint (verified)

Run `cs` from inside a git repository. `cs debug` prints resolved config/log paths.

**Core (Go) — repo root**
```bat
go build -o cs.exe .         :: or build.bat ; or  go build -o dist\cs.exe .
go test ./...                :: or test.bat / test.sh  (CI: go test -v ./...)
gofmt -w .                   :: format ; CI checks `gofmt -l .`
```
CI also runs **golangci-lint v1.60.1**. The `cs` binary is released via goreleaser
(`.goreleaser.yaml`).

**Desktop — `cd desktop` (Windows)**
```powershell
npm install
npm run lint        # eslint
npm run typecheck   # tsc --noEmit
npm run test        # vitest
npm run test:e2e    # playwright
npm run build       # electron-vite build
npm run dist        # electron-builder --win nsis  (installer)
```
Needs a current-protocol `cs.exe` (default `dist\cs.exe`; override with `CS_EXE`). Ignore
`selftest`/`cleanup` mentioned in `desktop/README.md` — those scripts are not in `package.json`.

**Web — `cd web`**
```powershell
npm run dev | npm run build | npm run lint
```

## Conventions

- **Platform-specific files use filename build constraints:** `*_windows.go`, `*_unix.go`,
  `*_other.go` (pervasive across terminal/attach/daemon/tmux/noconsole/runhost). Add or edit
  the matching variant rather than `runtime.GOOS` branches.
- **Tests live alongside source** as `*_test.go` (testify is available).
- **Windows-first repo:** prefer Windows paths and `.bat` scripts; `.sh` equivalents exist for
  Unix (`build.sh`, `test.sh`, `clean.sh`).
- `gh` is required for GitHub operations; tmux is required only on Unix/macOS/WSL.

## Key docs

- `docs/native-windows.md` — native Windows backend design, alternatives, gotchas (read before
  touching `session/winhost/` or Windows attach/AutoYes/persistence).
- `desktop/HANDOFF.md`, `desktop/README.md`, `desktop/PACKAGING.md` — desktop app.
- `README.md` — features, install, troubleshooting, configuration (profiles, sidebar modes,
  Copilot session browser).

## Gotchas / do-not-break

- Do not regress the Unix/tmux path when working on Windows features (and vice-versa); both
  sit behind `session.TerminalSession`.
- Sessions do **not** survive reboot or `cs reset`; `cs reset` clears instances, worktrees,
  the daemon/host, and the Copilot index.
- Keep desktop ↔ daemon protocol changes in sync (versioned; currently v3).
- `web/` is a static marketing site — don't treat it as an embedded app UI.

## Existing agent tooling

`.github/agents/` holds a feature-development workflow (`feature-developer` orchestrator +
`fd-*` subagents) and a `pr-creator`; `.github/skills/plan-review/` is a reusable skill.
Keep the `## HANGAR CODEBASE CONTEXT` block in those agent files consistent with this file.
