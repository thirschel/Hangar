<p align="center"><img src="assets/logo.png" width="320" alt="Hangar" /></p>

# Hangar

<p align="center"><strong>A hangar for all your copilots.</strong></p>
<p align="center">The native-Windows-first manager for your AI coding agents.</p>

<p align="center">
  <a href="https://github.com/thirschel/Hangar/actions/workflows/build.yml"><img src="https://github.com/thirschel/Hangar/actions/workflows/build.yml/badge.svg" alt="CI" /></a>
</p>

<p align="center">
  <a href="https://thirschel.github.io/Hangar/">Website</a> ·
  <a href="https://github.com/thirschel/Hangar">Repository</a>
</p>

[Hangar](https://thirschel.github.io/Hangar/) is a Windows desktop app for developers who run more than one AI coding agent at a time. Download the Hangar installer, launch Claude Code, Codex, Gemini, GitHub Copilot CLI, Aider, and other local assistants in isolated workspaces, and review their work before it ships. Under the hood, Hangar is powered by the `cs` core-daemon/session host, so agents keep working even when the desktop app or TUI is closed.

![Hangar Screenshot](assets/screenshot.png)

## Why Hangar

### Runs natively on Windows

No WSL, no tmux. The Hangar desktop app drives a background `cs session-host` that owns a real Windows console (ConPTY) per agent, talks over a named pipe, and renders terminal output via a VT emulator. Sessions survive app and TUI restarts.

### Supervise multiple agents at once

Claude Code, Codex, Gemini, GitHub Copilot CLI, and Aider side by side from one native Windows app, with the `cs` engine available as a terminal dashboard too.

### Isolated git worktrees

Every session on its own branch/worktree; tasks never collide.

### Review before you ship

Inspect each session's diff, then commit & push or checkout & pause.

### Background + AutoYes

Agents keep working and auto-accept prompts even while the desktop app or TUI is closed; pauses while you're attached.

### Attach / detach

Enter to attach, Ctrl+q to detach; Ctrl+c passes through to the agent.

<br />

https://github.com/user-attachments/assets/aef18253-e58f-4525-9032-f5a3d66c975a

<br />

## Installation

### Download (Windows desktop app)

Download `Hangar-Setup-<version>.exe` from [the latest Hangar release](https://github.com/thirschel/Hangar/releases/latest), then double-click it to install the Windows desktop app. Hangar uses an NSIS installer and auto-updates with `electron-updater`. Releases publish the Windows `.exe` installer as assets, replacing the old GoReleaser Go-binary release.

> **Current status:** no release is published yet — until then, build from source (below).

> **Unsigned installer:** Hangar is currently unsigned. Windows SmartScreen may warn on first run; choose **More info** → **Run anyway** if you trust this repository.

### Build from source

#### Daemon/CLI (`cs`)

Build the underlying `cs` daemon/CLI from this fork when you want the standalone TUI or while the desktop installer is not yet published:

```bat
:: Requires Go 1.25+ (https://go.dev/dl/), git, and an agent installed on Windows
:: Your agent must be resolvable, for example: where copilot
git clone https://github.com/thirschel/Hangar.git
cd Hangar
build.bat
```

`build.bat` produces `cs.exe` in the repo root. You can also build the same binary with:

```bat
go build -o dist\cs.exe .
```

Put `cs.exe` on your `PATH`, then run `cs` from within a git repository.

Your agent (for example GitHub Copilot CLI) must be installed on Windows and resolvable:

```bat
where copilot
```

How the native Windows build differs from the tmux backend:

- **Architecture.** A detached `cs session-host` process owns the agent consoles,
  so sessions keep running while the TUI is closed and are reattached when you
  reopen `cs`. Run `cs debug` to see the host's pipe, PID, protocol version, and
  live sessions.
- **Persistence scope.** Sessions survive **restarting the TUI**, but **not a
  reboot** or `cs reset` — the session host (and the consoles it owns) is a normal
  process that does not come back after a reboot. On the next launch `cs` recreates
  each missing session in its existing worktree.
- **Attach / detach.** Press <kbd>Enter</kbd> to attach to a session and
  <kbd>Ctrl</kbd>+<kbd>q</kbd> to detach back to the TUI. <kbd>Ctrl</kbd>+<kbd>c</kbd>
  passes through to the agent.
- **AutoYes** is owned by the session host, so approval prompts are auto-approved
  even while the TUI is closed, and it pauses automatically while you are attached.
  It recognises `claude`, `copilot`, `aider`, and `gemini` approval prompts.
- **Pause / Resume.** Pausing commits your changes and stops the session; resuming
  starts a **fresh** agent in the recreated worktree (the prior agent conversation
  is not restored).
- **Terminal tab.** The in-TUI Terminal tab is disabled on native Windows — use
  attach (<kbd>Enter</kbd>) instead.
- `gh` is still required for GitHub operations.

> **Architecture & handoff:** see [`docs/native-windows.md`](docs/native-windows.md) for the full
> design (session host, ConPTY + VT emulator, attach hand-off, host-side AutoYes), the alternatives
> considered, and notes for anyone extending the Windows backend.

#### Desktop app

Build the Windows desktop app from source after building the daemon binary:

```bat
git clone https://github.com/thirschel/Hangar.git
cd Hangar
go build -o dist\cs.exe .
cd desktop
npm install
npm run dist
```

The packaged installer is written to `desktop\release\Hangar-Setup-<version>.exe`. See [`desktop/PACKAGING.md`](desktop/PACKAGING.md) for packaging details.

#### Unix/macOS/WSL standalone release installs

The scripts below install the Hangar fork's standalone `cs` daemon/CLI from [`thirschel/Hangar`](https://github.com/thirschel/Hangar). Before extracting or running the downloaded archive, they fetch `checksums.txt`, `checksums.txt.sig`, and `checksums.txt.pem`; verify the checksum file with `cosign` when available; then require the archive SHA256 to match `checksums.txt`. If `cosign` is not installed, the scripts abort unless you explicitly acknowledge checksum-only verification with `--skip-signature-check` or `-SkipSignatureCheck`.

##### Homebrew

The Homebrew formula currently installs the upstream `claude-squad` package, not the Hangar fork:

```bash
brew install claude-squad
ln -s "$(brew --prefix)/bin/claude-squad" "$(brew --prefix)/bin/cs"
```

##### Shell script

```bash
curl -fsSL https://raw.githubusercontent.com/thirschel/Hangar/main/install.sh | bash
```

This puts the `cs` binary in `~/.local/bin`.

To use a custom name for the binary:

```bash
curl -fsSL https://raw.githubusercontent.com/thirschel/Hangar/main/install.sh | bash -s -- --name <your-binary-name>
```

PowerShell performs the same verify-then-run flow for the Windows standalone CLI:

```powershell
iwr https://raw.githubusercontent.com/thirschel/Hangar/main/install.ps1 -OutFile install.ps1
.\install.ps1
```

## The underlying daemon (`cs`)

Hangar is a thin Electron client over a Go core-daemon: `cs session-host`. The session host owns each workspace (git worktree + branch), ConPTY console, VT terminal state, diffs, persistence, and host-side AutoYes; the desktop app drives that engine through the native Windows backend.

The daemon has standalone value. You can build and run `cs` directly as a terminal UI without the desktop app, while the Electron client lives in [`desktop/`](desktop/). For architecture details, see [`docs/native-windows.md`](docs/native-windows.md). The Go module name, binary, subcommands, and state directory are `hangar`, `cs`, `cs debug`, `cs reset`, `cs session-host`, and `~/.hangar/`.

## Prerequisites

The Windows desktop installer needs no source-build toolchain. To use agents, install at least one local AI coding agent such as GitHub Copilot CLI, Claude Code, Codex, Gemini, or Aider.

Building from source requires:

- [Go 1.25+](https://go.dev/dl/) for the `cs` daemon/CLI and native Windows session host
- [Node.js 18+](https://nodejs.org/) and npm for the desktop app build
- [git](https://git-scm.com/) for worktree and branch management
- [gh](https://cli.github.com/) for GitHub operations
- An agent installed on Windows and resolvable from `PATH` (for example, `where copilot`)
- [tmux](https://github.com/tmux/tmux/wiki/Installing) on Unix/macOS/WSL only — not needed for the native Windows build

> **Note (WSL / Linux):** the AI agent you run (e.g. `claude`, `copilot`, `aider`) must be a
> **native Linux executable** that meets its own system requirements. GitHub Copilot CLI, for
> example, requires **glibc 2.28+** (Ubuntu 20.04+, Debian 10+, Fedora 29+) and **Node.js 22+**.
> See [Troubleshooting](#troubleshooting).

## Troubleshooting

These notes are for the underlying `cs` daemon/CLI engine used by Hangar.

**`error capturing pane content: exit status 1` / sessions die immediately.** This almost always
means the agent program exited the instant it launched, so its tmux session or Windows session-host console disappeared. Common
causes:

- The agent isn't installed in this environment. Check `command -v <program>` on Unix/macOS/WSL or `where <program>` on Windows (e.g. `where copilot`).
- On **WSL**, the agent is installed on **Windows**, not inside Linux. If `command -v <program>`
  points under `/mnt/c/...`, it's the Windows install and can't run in a Linux tmux pane — reinstall
  it natively inside WSL.
- The distro is too old for the agent. e.g. `copilot: ... version 'GLIBC_2.28' not found` means your
  glibc is older than the agent requires. Check `ldd --version` (need 2.28+) and upgrade/replace the
  WSL distro (e.g. `wsl --install -d Ubuntu-24.04`), then reinstall the agent with Node.js 22+.

Run `cs debug` to print the resolved config and log-file paths.

**Where are the logs?** Hangar's `cs` engine writes to `hangar.log` in the OS temp dir
(`/tmp/hangar.log` on Linux/WSL). On WSL that is the **Linux** `/tmp` — open it from inside WSL
(or via `\\wsl$\<distro>\tmp\hangar.log` from Windows), not `C:\tmp`.

**Where is state stored, and how do I reset it?** All state lives in `~/.hangar/`: `state.json`
holds your sessions/instances, `config.json` the configuration, and `daemon.pid` the autoyes daemon.
On **native Windows** the session host also writes `host.json` (its pipe/PID/version) and `host.lock`
there. Run `cs reset` to clear stored instances, remove worktrees, stop the daemon, and (on Windows)
shut down the session host and its running sessions — or delete `~/.hangar/` manually.

## Usage

This section documents the standalone `cs` TUI/CLI engine that powers Hangar.

```
Usage:
  cs [flags]
  cs [command]

Available Commands:
  completion  Generate the autocompletion script for the specified shell
  debug       Print debug information like config paths
  help        Help about any command
  reset       Reset all stored instances
  version     Print the version number of Hangar

Flags:
  -y, --autoyes          [experimental] If enabled, all instances will automatically accept prompts for claude code & aider
  -h, --help             help for Hangar
  -p, --program string   Program to run in new instances (e.g. 'aider --model ollama_chat/gemma3:1b')
```

Run the TUI with:

```bash
cs
```

NOTE: The default program is `claude` and we recommend using the latest version.

<br />

<b>Using Hangar's `cs` engine with other AI assistants:</b>

- For [Codex](https://github.com/openai/codex): Set your API key with `export OPENAI_API_KEY=<your_key>`
- Launch with specific assistants:
  - Codex: `cs -p "codex"`
  - Aider: `cs -p "aider ..."`
  - Gemini: `cs -p "gemini"`
- Make this the default by modifying the config file (locate with `cs debug`)

<br />

### Menu

The `cs` TUI menu at the bottom of the screen shows available commands:

#### Instance/Session Management

- `n` - Create a new session
- `N` - Create a new session with a prompt
- `D` - Kill (delete) the selected session
- `↑/j`, `↓/k` - Navigate between sessions
- `J/K` - Reorder sessions (Manual sidebar mode only)

##### Sidebar view

- `s` / `S` - Cycle the sidebar mode forward / backward: **Manual → Group by repo → Recent activity → Pinned-pending**. The active mode is shown in the sidebar title and persists across restarts.
- `f` - Cycle the session-only status filter: **All → Waiting → Busy → Idle → Paused**. The sidebar always shows per-status counts.
- `/` - Search/filter sessions by title or repo path. While searching, letters edit the query and only the arrow keys navigate; `enter` keeps the filter, `esc` clears it and restores your previous selection.

#### Actions

- `↵/o` - Attach to the selected session to reprompt
- `ctrl-q` - Detach from session
- `p` - Commit and push branch to github
- `c` - Checkout. Commits changes and pauses the session
- `r` - Resume a paused session
- `?` - Show help menu

#### Navigation

- `tab` - Switch between preview tab and diff tab
- `q` - Quit the application
- `shift-↓/↑` - scroll in diff view

##### Copilot Session Browser

- `b` - Open a full-screen browser for local GitHub Copilot CLI sessions discovered under `~/.copilot/session-state/`
- Type to live-search session metadata and conversation text
- `↑/↓` or `ctrl-k`/`ctrl-j` - Move selection
- `↵` - Resume the selected conversation with `copilot --resume=<id>` in a new isolated worktree for its original repo (confirms first when crossing repos)
- `esc` or `ctrl-c` - Close the browser
- `ctrl-r` - Force a re-scan and rebuild the local search index

The browser is read-only toward Copilot's session files. Its content search index is cached at `~/.hangar/copilot-index.json` and cleared by `cs reset`.

## Configuration

Hangar's `cs` engine stores its configuration in `~/.hangar/config.json`. You can find the exact path by running `cs debug`.

The sidebar animates rows when they move (reorder, sort, filter, pin). To turn animations off, set `"disable_sidebar_motion": true` in the config file. Motion is also automatically disabled when the terminal is very small or there are many visible workspaces.

### Profiles

Profiles let you define multiple named program configurations and switch between them when creating a new session. When more than one profile is defined, the session creation overlay shows a profile picker that you can navigate with `←`/`→`.

To configure profiles, add a `profiles` array to your config file and set `default_program` to the name of the profile to select by default:

```json
{
  "default_program": "claude",
  "profiles": [
    { "name": "claude", "program": "claude" },
    { "name": "codex", "program": "codex" },
    { "name": "aider", "program": "aider --model ollama_chat/gemma3:1b" }
  ]
}
```

Each profile has two fields:

| Field     | Description                                             |
| --------- | ------------------------------------------------------- |
| `name`    | Display name shown in the profile picker                |
| `program` | Shell command used to launch the agent for that profile |

If no profiles are defined, Hangar uses `default_program` directly as the launch command (the default is `claude`).

## FAQs

These FAQs are for the underlying `cs` daemon/CLI behavior.

### Failed to start new session

If you get an error like `failed to start new session: timed out waiting for tmux session`, update the
underlying program (ex. `claude`) to the latest version.

## How It Works

Hangar's Windows desktop app is the product surface; the `cs` daemon/CLI is the engine underneath it.

1. **Electron desktop app** in [`desktop/`](desktop/) to install and run Hangar as a Windows app
2. **`cs session-host` on native Windows** to own ConPTY consoles, VT terminal state, workspaces, diffs, persistence, and host-side AutoYes
3. **tmux** to create isolated terminal sessions for each agent on Unix/macOS/WSL; on **native Windows**, the background **session host** replaces tmux
4. **git worktrees** to isolate codebases so each session works on its own branch
5. A simple `cs` TUI interface for standalone terminal navigation and management

## License

[AGPL-3.0](https://github.com/thirschel/Hangar/blob/main/LICENSE.md)

Hangar is a fork of [claude-squad](https://github.com/smtg-ai/claude-squad), licensed under AGPL-3.0.

## Star History

[![Star History Chart](https://api.star-history.com/svg?repos=thirschel/Hangar&type=Date)](https://www.star-history.com/#thirschel/Hangar&Date)
