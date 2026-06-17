# Claude Squad [![CI](https://github.com/smtg-ai/claude-squad/actions/workflows/build.yml/badge.svg)](https://github.com/smtg-ai/claude-squad/actions/workflows/build.yml) [![GitHub Release](https://img.shields.io/github/v/release/smtg-ai/claude-squad)](https://github.com/smtg-ai/claude-squad/releases/latest)

[Claude Squad](https://smtg-ai.github.io/claude-squad/) is a terminal app that manages multiple [Claude Code](https://github.com/anthropics/claude-code), [Codex](https://github.com/openai/codex), [Gemini](https://github.com/google-gemini/gemini-cli) (and other local agents including [Aider](https://github.com/Aider-AI/aider)) in separate workspaces, allowing you to work on multiple tasks simultaneously.


![Claude Squad Screenshot](assets/screenshot.png)

### Highlights
- Complete tasks in the background (including yolo / auto-accept mode!)
- Manage instances and tasks in one terminal window
- Review changes before applying them, checkout changes before pushing them
- Each task gets its own isolated git workspace, so no conflicts

<br />

https://github.com/user-attachments/assets/aef18253-e58f-4525-9032-f5a3d66c975a

<br />

### Installation

Both Homebrew and manual installation will install Claude Squad as `cs` on your system.

#### Homebrew

```bash
brew install claude-squad
ln -s "$(brew --prefix)/bin/claude-squad" "$(brew --prefix)/bin/cs"
```

#### Manual

Claude Squad can also be installed by running the following command:

```bash
curl -fsSL https://raw.githubusercontent.com/smtg-ai/claude-squad/main/install.sh | bash
```

This puts the `cs` binary in `~/.local/bin`.

To use a custom name for the binary:

```bash
curl -fsSL https://raw.githubusercontent.com/smtg-ai/claude-squad/main/install.sh | bash -s -- --name <your-binary-name>
```

#### Windows (native, from this fork)

This fork runs **natively on Windows without WSL or tmux**. Each agent runs in a
real Windows console (ConPTY) owned by a background **session host** process; the
`cs` TUI talks to it over a per-user named pipe and renders each session through a
VT emulator (the equivalent of tmux's `capture-pane`). Build `cs.exe` from source:

```bat
:: Requires Go 1.25+ (https://go.dev/dl/) and git
git clone https://github.com/thirschel/claude-squad.git
cd claude-squad
build.bat
```

`build.bat` produces `cs.exe` in the repo root. Put it on your `PATH` and run `cs`
from within a git repository. Your agent (e.g. GitHub Copilot CLI) must be installed
on Windows and resolvable (`where copilot`).

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

### Prerequisites

- [tmux](https://github.com/tmux/tmux/wiki/Installing) (Unix/macOS/WSL only — not needed for the native Windows build)
- [gh](https://cli.github.com/)

> **Note (WSL / Linux):** the AI agent you run (e.g. `claude`, `copilot`, `aider`) must be a
> **native Linux executable** that meets its own system requirements. GitHub Copilot CLI, for
> example, requires **glibc 2.28+** (Ubuntu 20.04+, Debian 10+, Fedora 29+) and **Node.js 22+**.
> See [Troubleshooting](#troubleshooting).

### Troubleshooting

**`error capturing pane content: exit status 1` / sessions die immediately.** This almost always
means the agent program exited the instant it launched, so its tmux session disappeared. Common
causes:

- The agent isn't installed in this environment. Check `command -v <program>` (e.g. `copilot`).
- On **WSL**, the agent is installed on **Windows**, not inside Linux. If `command -v <program>`
  points under `/mnt/c/...`, it's the Windows install and can't run in a Linux tmux pane — reinstall
  it natively inside WSL.
- The distro is too old for the agent. e.g. `copilot: ... version 'GLIBC_2.28' not found` means your
  glibc is older than the agent requires. Check `ldd --version` (need 2.28+) and upgrade/replace the
  WSL distro (e.g. `wsl --install -d Ubuntu-24.04`), then reinstall the agent with Node.js 22+.

Run `cs debug` to print the resolved config and log-file paths.

**Where are the logs?** Claude Squad writes to `claudesquad.log` in the OS temp dir
(`/tmp/claudesquad.log` on Linux/WSL). On WSL that is the **Linux** `/tmp` — open it from inside WSL
(or via `\\wsl$\<distro>\tmp\claudesquad.log` from Windows), not `C:\tmp`.

**Where is state stored, and how do I reset it?** All state lives in `~/.claude-squad/`: `state.json`
holds your sessions/instances, `config.json` the configuration, and `daemon.pid` the autoyes daemon.
On **native Windows** the session host also writes `host.json` (its pipe/PID/version) and `host.lock`
there. Run `cs reset` to clear stored instances, remove worktrees, stop the daemon, and (on Windows)
shut down the session host and its running sessions — or delete `~/.claude-squad/` manually.

### Usage

```
Usage:
  cs [flags]
  cs [command]

Available Commands:
  completion  Generate the autocompletion script for the specified shell
  debug       Print debug information like config paths
  help        Help about any command
  reset       Reset all stored instances
  version     Print the version number of claude-squad

Flags:
  -y, --autoyes          [experimental] If enabled, all instances will automatically accept prompts for claude code & aider
  -h, --help             help for claude-squad
  -p, --program string   Program to run in new instances (e.g. 'aider --model ollama_chat/gemma3:1b')
```

Run the application with:

```bash
cs
```
NOTE: The default program is `claude` and we recommend using the latest version.

<br />

<b>Using Claude Squad with other AI assistants:</b>
- For [Codex](https://github.com/openai/codex): Set your API key with `export OPENAI_API_KEY=<your_key>`
- Launch with specific assistants:
   - Codex: `cs -p "codex"`
   - Aider: `cs -p "aider ..."`
   - Gemini: `cs -p "gemini"`
- Make this the default, by modifying the config file (locate with `cs debug`)

<br />

#### Menu
The menu at the bottom of the screen shows available commands: 

##### Instance/Session Management
- `n` - Create a new session
- `N` - Create a new session with a prompt
- `D` - Kill (delete) the selected session
- `↑/j`, `↓/k` - Navigate between sessions
- `J/K` - Reorder sessions (Manual sidebar mode only)

##### Sidebar view
- `s` / `S` - Cycle the sidebar mode forward / backward: **Manual → Group by repo → Recent activity → Pinned-pending**. The active mode is shown in the sidebar title and persists across restarts.
- `/` - Search/filter sessions by title or repo path. While searching, letters edit the query and only the arrow keys navigate; `enter` keeps the filter, `esc` clears it and restores your previous selection.

##### Actions
- `↵/o` - Attach to the selected session to reprompt
- `ctrl-q` - Detach from session
- `p` - Commit and push branch to github
- `c` - Checkout. Commits changes and pauses the session
- `r` - Resume a paused session
- `?` - Show help menu

##### Navigation
- `tab` - Switch between preview tab and diff tab
- `q` - Quit the application
- `shift-↓/↑` - scroll in diff view

### Configuration

Claude Squad stores its configuration in `~/.claude-squad/config.json`. You can find the exact path by running `cs debug`.

The sidebar animates rows when they move (reorder, sort, filter, pin). To turn animations off, set `"disable_sidebar_motion": true` in the config file. Motion is also automatically disabled when the terminal is very small or there are many visible workspaces.

#### Profiles

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

| Field     | Description                                              |
|-----------|----------------------------------------------------------|
| `name`    | Display name shown in the profile picker                 |
| `program` | Shell command used to launch the agent for that profile  |

If no profiles are defined, Claude Squad uses `default_program` directly as the launch command (the default is `claude`).

### FAQs

#### Failed to start new session

If you get an error like `failed to start new session: timed out waiting for tmux session`, update the
underlying program (ex. `claude`) to the latest version.

### How It Works

1. **tmux** to create isolated terminal sessions for each agent (on **native
   Windows**, a background **session host** owns a ConPTY console per agent instead)
2. **git worktrees** to isolate codebases so each session works on its own branch
3. A simple TUI interface for easy navigation and management

### License

[AGPL-3.0](LICENSE.md)

### Star History

[![Star History Chart](https://api.star-history.com/svg?repos=smtg-ai/claude-squad&type=Date)](https://www.star-history.com/#smtg-ai/claude-squad&Date)
