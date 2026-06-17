<#
.SYNOPSIS
  Build and run the Hangar desktop app + Go daemon from THIS worktree.

.DESCRIPTION
  The daemon (cs.exe) is a per-user *singleton*: the desktop app connects to
  whichever daemon already owns the named pipe (\\.\pipe\hangar-host-<SID>).
  When you work across multiple git worktrees, a stale daemon/app from another
  checkout gets reused, so you end up testing the WRONG code — e.g. the banner
  "daemon is v4 — the desktop app needs v5".

  This script makes the *current worktree* authoritative:
    1. Rebuilds this worktree's dist\cs.exe (+ root cs.exe) from Go source.
    2. Stops any running Hangar desktop app instances (any checkout).
    3. Stops the running session-host daemon, freeing the singleton pipe.
    4. Launches `npm run dev` from this worktree with CS_EXE pinned to its binary.

  IMPORTANT: run this from a NORMAL terminal, not from inside a Hangar
  agent terminal. Step 3 restarts the daemon and briefly interrupts every live
  agent session (they auto-revive on next attach). The script refuses to run if
  it detects it is itself running inside a daemon-owned ConPTY (override: -Force).

.PARAMETER SkipBuild
  Skip the Go rebuild; restart the daemon/app with the existing dist\cs.exe.

.PARAMETER NoApp
  Build and free the pipe, but do not launch the app (headless daemon refresh).

.PARAMETER GoExe
  Path to go.exe. Defaults to the portable toolchain
  ($env:TEMP\goroot125\go\bin\go.exe), then falls back to `go` on PATH.

.PARAMETER Force
  Proceed even if the script appears to be running inside a Hangar agent
  terminal (i.e. killing the daemon would kill this very terminal).

.EXAMPLE
  .\desktop\scripts\dev-worktree.ps1
  # rebuild + relaunch everything from this worktree

.EXAMPLE
  .\desktop\scripts\dev-worktree.ps1 -SkipBuild
  # just point the app/daemon at this worktree without rebuilding Go

.EXAMPLE
  .\desktop\scripts\dev-worktree.ps1 -NoApp
  # rebuild the daemon and free the pipe; launch the app yourself later
#>
[CmdletBinding()]
param(
  [switch]$SkipBuild,
  [switch]$NoApp,
  [string]$GoExe,
  [switch]$Force
)

$ErrorActionPreference = 'Stop'

function Write-Step([string]$m) { Write-Host "==> $m" -ForegroundColor Cyan }
function Write-Ok([string]$m)   { Write-Host "    $m" -ForegroundColor Green }
function Write-Note([string]$m) { Write-Host "    $m" -ForegroundColor Yellow }

# Walk the parent-process chain to see if we're running inside the daemon's
# ConPTY (cs.exe). If so, stopping the daemon would kill this terminal.
function Test-InsideDaemon {
  $p = Get-CimInstance Win32_Process -Filter "ProcessId=$PID" -ErrorAction SilentlyContinue
  for ($i = 0; $i -lt 12 -and $p; $i++) {
    $parent = Get-CimInstance Win32_Process -Filter "ProcessId=$($p.ParentProcessId)" -ErrorAction SilentlyContinue
    if (-not $parent) { break }
    if ($parent.Name -eq 'cs.exe') { return $true }
    $p = $parent
  }
  return $false
}

# --- 1. Locate the worktree root (prefer git; fall back to script path) -------
$root = $null
try { $root = (& git -C $PSScriptRoot rev-parse --show-toplevel 2>$null) } catch { }
if (-not $root) { $root = (Resolve-Path (Join-Path $PSScriptRoot '..\..')).Path }
$root = ($root -replace '/', '\').TrimEnd('\')
$desktop = Join-Path $root 'desktop'
if (-not (Test-Path (Join-Path $desktop 'package.json'))) {
  throw "Could not find desktop\package.json under '$root'. Run this from inside a worktree."
}
Write-Step "Worktree: $root"

# --- 2. Resolve the Go toolchain ---------------------------------------------
if (-not $GoExe) {
  $portable = Join-Path $env:TEMP 'goroot125\go\bin\go.exe'
  if (Test-Path $portable) { $GoExe = $portable }
  else { $GoExe = (Get-Command go -ErrorAction SilentlyContinue).Source }
}
if (-not $GoExe -or -not (Test-Path $GoExe)) {
  throw "No Go toolchain found. Pass -GoExe <path-to-go.exe>."
}

# Report the proto version we are about to build (a quick sanity check).
$protoVer = '?'
$verLine = Select-String -Path (Join-Path $root 'session\winhost\proto\proto.go') -Pattern 'const Version\s*=\s*(\d+)'
if ($verLine) { $protoVer = $verLine.Matches[0].Groups[1].Value }
Write-Step "Go: $GoExe  (source proto v$protoVer)"

# --- 3. Resolve binary path (build happens AFTER the daemon is stopped, below,
#         because Windows can't overwrite a running .exe) -----------------------
$csExe = Join-Path $root 'dist\cs.exe'
if ($SkipBuild -and -not (Test-Path $csExe)) {
  throw "Missing $csExe. Run once without -SkipBuild."
}

# --- 4. Safety: don't kill the daemon if it hosts THIS terminal ---------------
if ((Test-InsideDaemon) -and -not $Force) {
  throw ("This terminal is running inside a Hangar agent session, so stopping " +
         "the daemon would kill it. Re-run from an external PowerShell, or pass -Force.")
}

# --- 5. Stop existing Hangar desktop app instances (any checkout) -------
Write-Step "Stopping running Hangar app instances ..."
$apps = @()
$apps += Get-CimInstance Win32_Process -Filter "Name='electron.exe'" -ErrorAction SilentlyContinue |
          Where-Object { $_.CommandLine -and $_.CommandLine -like '*Hangar*' }
$apps += Get-CimInstance Win32_Process -Filter "Name='Hangar.exe'" -ErrorAction SilentlyContinue
$killedApp = $false
foreach ($p in ($apps | Sort-Object ProcessId -Unique)) {
  try { Stop-Process -Id $p.ProcessId -Force -ErrorAction Stop; $killedApp = $true } catch { }
}
if ($killedApp) { Write-Ok "App instances stopped." } else { Write-Note "No app instances running." }

# --- 6. Stop the daemon (free the singleton pipe) -----------------------------
Write-Step "Stopping the session-host daemon (frees the pipe) ..."
$daemonPids = @()
$daemonPids += (Get-CimInstance Win32_Process -Filter "Name='cs.exe'" -ErrorAction SilentlyContinue |
                 Where-Object { $_.CommandLine -and $_.CommandLine -like '*session-host*' }).ProcessId
$hostJson = Join-Path $env:USERPROFILE '.hangar\host.json'
if (Test-Path $hostJson) {
  try { $daemonPids += (Get-Content $hostJson -Raw | ConvertFrom-Json).pid } catch { }
}
$daemonPids = $daemonPids | Where-Object { $_ } | Sort-Object -Unique
$killedDaemon = $false
foreach ($procId in $daemonPids) {
  try { Stop-Process -Id $procId -Force -ErrorAction Stop; $killedDaemon = $true } catch { }
}
if ($killedDaemon) {
  Write-Ok "Daemon stopped (pids: $($daemonPids -join ', '))."
  Write-Note "Live agent sessions were interrupted; they auto-revive on next attach."
}
else { Write-Note "No daemon running." }

# Give Windows a moment to tear down the named pipe and release the exe lock.
Start-Sleep -Milliseconds 800

# --- 7. Build the daemon now that the old process has released dist\cs.exe -----
if ($SkipBuild) {
  Write-Note "Skipping build (-SkipBuild); using existing dist\cs.exe."
}
else {
  Write-Step "Building dist\cs.exe + cs.exe (proto v$protoVer) ..."
  Push-Location $root
  try {
    $env:GOTOOLCHAIN = 'local'; $env:GOFLAGS = '-mod=mod'
    & $GoExe build -o $csExe .
    if ($LASTEXITCODE -ne 0) { throw "go build dist\cs.exe failed ($LASTEXITCODE)" }
    & $GoExe build -o (Join-Path $root 'cs.exe') .
    if ($LASTEXITCODE -ne 0) { throw "go build cs.exe failed ($LASTEXITCODE)" }
  }
  finally { Pop-Location }
  Write-Ok "Built."
}

# --- 8. Launch the app from this worktree ------------------------------------
if ($NoApp) {
  Write-Step "Done (-NoApp). Next launch with CS_EXE=$csExe spawns this v$protoVer daemon."
  return
}

Push-Location $desktop
try {
  if (-not (Test-Path (Join-Path $desktop 'node_modules'))) {
    Write-Step "Installing npm dependencies (first run) ..."
    npm install
    if ($LASTEXITCODE -ne 0) { throw "npm install failed ($LASTEXITCODE)" }
  }
  $env:CS_EXE = $csExe
  Write-Step "Launching app (CS_EXE pinned). Status bar should read 'Protocol v$protoVer'."
  Write-Note "Ctrl+C here stops the dev server. Closing the app window also exits."
  npm run dev
}
finally { Pop-Location }
