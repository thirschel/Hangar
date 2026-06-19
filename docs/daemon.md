# The `cs` daemon & CLI

This is the full reference for the standalone `cs` daemon/CLI engine that powers Hangar. Use it when you want the terminal TUI directly, need native-Windows session-host details, or are installing the standalone engine outside the Windows desktop app.

## Native Windows build details

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

**The "New workspace" dialog gets stuck on "Creating…" and never closes (native Windows desktop
app).** Creating a workspace whose agent is a **PowerShell profile function** (e.g. a `cpa` wrapper for
copilot) runs a one-off PowerShell probe that loads your profile to confirm the command exists. On
locked-down machines that profile can hang or be very slow (endpoint security, slow/network module
imports), which previously blocked the create request indefinitely. The probe is now bounded (~30s)
and the desktop control client times out long-running requests, so the dialog surfaces an error
instead of hanging. If you hit the probe timeout (`agent probe timed out` in `host.log`), check your
PowerShell profile (`$PROFILE`) for slow or hanging startup, or set the agent to a program on `PATH`.

**Blank Agent and/or Terminal pane on native Windows (desktop app).** A new workspace opens but the
Agent pane — and a freshly opened Terminal — stay completely empty. This means the ConPTY child
(the agent, or the shell) exited or produced no output. When the host can tell the child has exited
it now prints `[agent process exited (code N) — see host.log via Settings → Diagnostics]` into the
pane instead of leaving it blank; the matching `conpty exited … (no output produced)` line in
`host.log` has the exit code and lifetime. On a **locked-down / corporate** machine this is commonly
endpoint-security (EDR), AppLocker, or antivirus blocking pseudo-console/process creation. To
diagnose: open **Settings → Diagnostics** in the desktop app to read/open `host.log`, and turn on
**Verbose logging (`HANGAR_DEBUG`)** there (restart the app so the session host re-spawns with it),
then reproduce and check `host.log` for the `conpty started` / `conpty exited` / `attach …` lines.

**Where are the logs?** The `cs` engine writes `hangar.log` to the OS temp dir (`/tmp/hangar.log` on
Linux/WSL — that is the **Linux** `/tmp`, openable from inside WSL or via
`\\wsl$\<distro>\tmp\hangar.log` from Windows, not `C:\tmp`). On **native Windows** the session host
writes `~/.hangar/host.log` (ConPTY session lifecycle, attach, and — with `HANGAR_DEBUG` set —
verbose per-read detail) and the desktop app writes `~/.hangar/desktop.log` (attach/shell/host-spawn
diagnostics). The desktop's **Settings → Diagnostics** tab opens or tails any of the three and toggles
`HANGAR_DEBUG` for the next session-host launch.

**Blank or garbled terminals in the desktop app (the pane renders nothing despite the agent
running).** If `desktop.log` shows the bytes arriving (`[renderer] TermView first data` / `first write
done`) but the pane stays empty, the terminal layer is failing to paint. The usual cause is
**software compositing** — most often an **RDP/VDI session** (no hardware GPU) or hardware acceleration
disabled by policy/drivers — where xterm updates the DOM but Chromium's software compositor doesn't
flush the paint until a reflow (the tell-tale sign is that **resizing the window/pane makes the text
appear**). Only xterm's layered screen is affected, which is why the rest of the app still paints.

The app now **detects software compositing** (logged as `softwareCompositing` in the startup
`gpu status` line of `desktop.log`) and **forces repaints** automatically while a terminal is attached,
so it should paint without a manual resize. Note that **"Disable hardware acceleration" does not help
on RDP** — there is no GPU to disable, so it leaves you in the same software-compositing path; the
forced-repaint behaviour is what fixes it. If a pane is still blank, resize the window once (it will
repaint) and capture the `gpu status` line for a bug report.

**Where is state stored, and how do I reset it?** All state lives in `~/.hangar/`: `state.json`
holds your sessions/instances, `config.json` the configuration, and `daemon.pid` the autoyes daemon.
On **native Windows** the session host also writes `host.json` (its pipe/PID/version) and `host.lock`
there. Run `cs reset` to clear stored instances, remove worktrees, stop the daemon, and (on Windows)
shut down the session host and its running sessions — or delete `~/.hangar/` manually.

## FAQs

These FAQs are for the underlying `cs` daemon/CLI behavior.

### Failed to start new session

If you get an error like `failed to start new session: timed out waiting for tmux session`, update the
underlying program (ex. `claude`) to the latest version.

## How It Works

Hangar's Windows desktop app is the product surface; the `cs` daemon/CLI is the engine underneath it.

1. **Electron desktop app** in [`desktop/`](../desktop/) to install and run Hangar as a Windows app
2. **`cs session-host` on native Windows** to own ConPTY consoles, VT terminal state, workspaces, diffs, persistence, and host-side AutoYes
3. **tmux** to create isolated terminal sessions for each agent on Unix/macOS/WSL; on **native Windows**, the background **session host** replaces tmux
4. **git worktrees** to isolate codebases so each session works on its own branch
5. A simple `cs` TUI interface for standalone terminal navigation and management

## Unix/macOS/WSL standalone release installs

The scripts below install the Hangar fork's standalone `cs` daemon/CLI from [`thirschel/Hangar`](https://github.com/thirschel/Hangar). Before extracting or running the downloaded archive, they fetch `checksums.txt`, `checksums.txt.sig`, and `checksums.txt.pem`; verify the checksum file with `cosign` when available; then require the archive SHA256 to match `checksums.txt`. If `cosign` is not installed, the scripts abort unless you explicitly acknowledge checksum-only verification with `--skip-signature-check` or `-SkipSignatureCheck`.

### Homebrew

The Homebrew formula currently installs the upstream `claude-squad` package, not the Hangar fork:

```bash
brew install claude-squad
ln -s "$(brew --prefix)/bin/claude-squad" "$(brew --prefix)/bin/cs"
```

### Shell script

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
