# Hangar Desktop (Electron, Windows)

> **New here? Read [`HANDOFF.md`](./HANDOFF.md) first** — it covers what's built, where it lives, how to
> build/test, the quirks/gotchas, and what's outstanding.

Electron + TypeScript + React + Vite **thin client** for the Hangar Go **core daemon**
(`cs session-host`, protocol **v3**). The app talks to the per-user SID named pipe (length-prefixed
JSON-RPC), owns no durable state itself, and renders each workspace's agent as a live xterm.js
terminal. The daemon owns the workspace lifecycle (git worktree + branch + ConPTY session), diffs,
persistence, and host-side AutoYes.

## Status

- **E0 (done):** scaffold + typed pipe client + three-pane shell.
- **E1 (done):** core-daemon workspace RPC in Go (`claude-squad`, branch `desktop-core-daemon`):
  `ListWorkspaces/CreateWorkspace/GetWorkspace/ArchiveWorkspace/WorkspaceDiff/SetWorkspaceAutoYes`.
- **E2 (this app — MVP core shell):** sidebar lists/creates/archives workspaces; the center pane is
  the selected workspace's live agent terminal + a composer + an AutoYes toggle; the right panel
  shows changed files and a per-file diff. Builds/typechecks/lints clean and boots; **end-to-end
  needs a v2 daemon** (see below).

Reference seam (still runnable): `protocol.js` / `selftest.js` (headless), `src/main/host-client.ts`
(typed client), `src/{main,preload,renderer}`.

## Prereqs

- Windows, Node 18+, and a **v2** `cs.exe` (build from the repo root with `go build -o dist\cs.exe .`).
  Default daemon path is the repo-root `dist\cs.exe` (for example `D:\dev\Hangar\dist\cs.exe`); override with `CS_EXE`.

## ⚠️ One-time: the daemon must be v2

The desktop app needs the v2 daemon. If an older v1 `cs`/host is running (it holds the singleton
pipe), retire it first:

```powershell
# build the v2 binary from the repo root (desktop-core-daemon branch)
go build -o dist\cs.exe .
cs reset            # stop any stale v1 host (and the TUI), then close it
```

The app shows a clear banner if it connects to a v1 host.

## Run

```powershell
cd D:\dev\Hangar\desktop
npm install
npm run dev
```

Then: click **+** in the sidebar → pick a git repo, give it a title (and optionally an agent like
`copilot` and a base branch) → **Create**. The agent boots in the center terminal; type in it or use
the composer. Create more workspaces to run agents in parallel; switch between them in the sidebar;
the right panel shows each one's changed files and diff; **×** archives a workspace (removes the
worktree, keeps the branch). Close and reopen the app — workspaces persist (the daemon keeps them).

## Validate

```powershell
npm run typecheck
npm run lint
npm run build
npm run selftest   # headless protocol seam check
npm run cleanup    # remove any stray poc-* sessions
```

Later phases: rich diff (Monaco) + Create PR + checks/merge (E3), per-workspace run/preview (E4),
tray/global-hotkeys/notifications/multi-window (E5), packaging/auto-update/signing (E6). See the
parent plan in `PLAN.md`.

