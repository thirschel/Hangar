# Claude Squad Windows Installation Script
# PowerShell version of install.sh

param(
    [string]$Name = "cs",
    [string]$Version = "latest",
    [string]$BinDir = "$env:LOCALAPPDATA\bin",
    [switch]$SkipSignatureCheck
)

# Hard-coded Hangar release signing public key.
# The matching private key is stored as the MINISIGN_KEY GitHub Actions secret.
# Also committed (public key only) to keys/hangar-release.pub in this repository.
# REPLACE this placeholder with the real key after running:
#   minisign -G -p keys/hangar-release.pub -s hangar-release.key
$HANGAR_PUBKEY = "RWSxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx=="

$FORK_OWNER = "thirschel"
$FORK_REPO  = "Hangar"

# Pinned minisign version for Windows bootstrap (used only when minisign is not on PATH).
# DO NOT change MINISIGN_PINNED_VERSION without also updating MINISIGN_BOOTSTRAP_SHA256.
# Obtain the correct SHA256 from: https://github.com/jedisct1/minisign/releases
$MINISIGN_PINNED_VERSION    = "0.11"
$MINISIGN_BOOTSTRAP_SHA256  = "0000000000000000000000000000000000000000000000000000000000000000"  # PLACEHOLDER

function Write-Status {
    param([string]$Message, [string]$Type = "Info")
    $color = switch ($Type) {
        "Success" { "Green" }
        "Warning" { "Yellow" }
        "Error" { "Red" }
        default { "White" }
    }
    Write-Host $Message -ForegroundColor $color
}

function Test-CommandExists {
    param([string]$Command)
    $null -ne (Get-Command $Command -ErrorAction SilentlyContinue)
}

function Install-GitHubCLI {
    Write-Status "Installing GitHub CLI..." "Info"

    if (Test-CommandExists "winget") {
        try {
            winget install --id GitHub.cli --silent
            Write-Status "GitHub CLI installed successfully via winget" "Success"
            return $true
        }
        catch {
            Write-Status "Failed to install GitHub CLI via winget: $($_.Exception.Message)" "Warning"
        }
    }

    if (Test-CommandExists "choco") {
        try {
            choco install gh -y
            Write-Status "GitHub CLI installed successfully via chocolatey" "Success"
            return $true
        }
        catch {
            Write-Status "Failed to install GitHub CLI via chocolatey: $($_.Exception.Message)" "Warning"
        }
    }

    Write-Status "Please install GitHub CLI manually from https://cli.github.com/" "Warning"
    return $false
}

function Install-WindowsTerminal {
    Write-Status "Installing Windows Terminal..." "Info"

    if (Test-CommandExists "winget") {
        try {
            winget install --id Microsoft.WindowsTerminal --silent
            Write-Status "Windows Terminal installed successfully" "Success"
            return $true
        }
        catch {
            Write-Status "Failed to install Windows Terminal: $($_.Exception.Message)" "Warning"
        }
    }

    Write-Status "Please install Windows Terminal from Microsoft Store" "Warning"
    return $false
}

function Test-Dependencies {
    Write-Status "Checking dependencies..." "Info"

    $allDepsOk = $true

    # Check GitHub CLI
    if (-not (Test-CommandExists "gh")) {
        Write-Status "GitHub CLI not found. Installing..." "Warning"
        if (-not (Install-GitHubCLI)) {
            $allDepsOk = $false
        }
    } else {
        Write-Status "GitHub CLI is already installed" "Success"
    }

    # Check Windows Terminal (optional but recommended)
    if (-not (Test-CommandExists "wt")) {
        Write-Status "Windows Terminal not found. Installing..." "Warning"
        Install-WindowsTerminal  # Don't fail if this doesn't work
    } else {
        Write-Status "Windows Terminal is already installed" "Success"
    }

    return $allDepsOk
}

function Get-LatestVersion {
    try {
        $apiUrl = "https://api.github.com/repos/$FORK_OWNER/$FORK_REPO/releases"
        $releases = Invoke-RestMethod -Uri $apiUrl -Method Get

        if ($releases -and $releases.Count -gt 0) {
            $latestVersion = $releases[0].tag_name -replace "^v", ""
            return $latestVersion
        }
        else {
            throw "No releases found"
        }
    }
    catch {
        Write-Status "Failed to get latest version: $($_.Exception.Message)" "Error"
        throw
    }
}

function Get-Architecture {
    $arch = [System.Environment]::GetEnvironmentVariable("PROCESSOR_ARCHITECTURE")
    switch ($arch) {
        "AMD64" { return "amd64" }
        "ARM64" { return "arm64" }
        default { return "amd64" }
    }
}

function Install-Minisign {
    param([string]$TempDir)

    # Bootstrap minisign.exe from a pinned release, verifying its SHA256 before use.
    # This is a single-level bootstrap: we trust the pinned hash hard-coded above.
    # Update MINISIGN_PINNED_VERSION and MINISIGN_BOOTSTRAP_SHA256 when rotating.
    $zipName    = "minisign-win64.zip"
    $zipUrl     = "https://github.com/jedisct1/minisign/releases/download/$MINISIGN_PINNED_VERSION/$zipName"
    $zipPath    = Join-Path $TempDir $zipName
    $extractDir = Join-Path $TempDir "minisign-bootstrap"

    Write-Status "Downloading minisign $MINISIGN_PINNED_VERSION for bootstrapping..." "Info"
    try {
        Invoke-WebRequest -Uri $zipUrl -OutFile $zipPath -UseBasicParsing
    }
    catch {
        throw "Failed to download minisign for bootstrap: $($_.Exception.Message)"
    }

    $actualHash = (Get-FileHash $zipPath -Algorithm SHA256).Hash.ToLower()
    if ($actualHash -ne $MINISIGN_BOOTSTRAP_SHA256.ToLower()) {
        throw "FATAL: minisign bootstrap SHA256 mismatch!`n  Expected: $MINISIGN_BOOTSTRAP_SHA256`n  Got:      $actualHash`nAborting — update MINISIGN_BOOTSTRAP_SHA256 if you intentionally changed the pinned version."
    }

    Expand-Archive -Path $zipPath -DestinationPath $extractDir -Force
    $exe = Get-ChildItem -Recurse -Filter "minisign.exe" $extractDir | Select-Object -First 1
    if (-not $exe) { throw "minisign.exe not found in bootstrap archive." }

    Write-Status "minisign bootstrap verified (SHA256 OK)" "Success"
    return $exe.FullName
}

function Download-And-Verify-Release {
    param(
        [string]$Version,
        [string]$Architecture,
        [string]$TempDir,
        [bool]$SkipSig = $false
    )

    $releaseBase  = "https://github.com/$FORK_OWNER/$FORK_REPO/releases/download/v$Version"
    $archiveName  = "hangar_${Version}_windows_${Architecture}.zip"
    $sumsFile     = "checksums.txt"
    $sigFile      = "checksums.txt.minisig"
    $archivePath  = Join-Path $TempDir $archiveName
    $sumsPath     = Join-Path $TempDir $sumsFile
    $sigPath      = Join-Path $TempDir $sigFile

    # Step 1: Download archive, checksums, and signature.
    Write-Status "Downloading $archiveName ..." "Info"
    try {
        Invoke-WebRequest -Uri "$releaseBase/$archiveName" -OutFile $archivePath -UseBasicParsing
        Invoke-WebRequest -Uri "$releaseBase/$sumsFile"    -OutFile $sumsPath    -UseBasicParsing
        Invoke-WebRequest -Uri "$releaseBase/$sigFile"     -OutFile $sigPath     -UseBasicParsing
    }
    catch {
        Write-Status "Download failed: $($_.Exception.Message)" "Error"
        throw
    }
    Write-Status "Download completed" "Success"

    # Step 2: Verify minisign signature over checksums.txt (fails closed by default).
    if ($SkipSig) {
        Write-Status "" "Warning"
        Write-Status "WARNING: -SkipSignatureCheck specified. Minisign verification SKIPPED." "Warning"
        Write-Status "         Integrity is HTTPS-only. Use only for testing/debugging." "Warning"
        Write-Status "" "Warning"
    }
    else {
        $minisignExe = (Get-Command minisign -ErrorAction SilentlyContinue)?.Source
        if (-not $minisignExe) {
            Write-Status "minisign not found on PATH — bootstrapping from pinned release..." "Info"
            $minisignExe = Install-Minisign -TempDir $TempDir
        }

        & $minisignExe -Vm $sumsPath -P $HANGAR_PUBKEY -x $sigPath
        if ($LASTEXITCODE -ne 0) {
            throw "FATAL: Checksum file signature verification FAILED. Aborting."
        }
        Write-Status "✓ Minisign signature verified." "Success"
    }

    # Step 3: Verify SHA256 of archive against the (now-verified) checksums.txt.
    $expectedLine = (Get-Content $sumsPath | Where-Object { $_ -match [regex]::Escape($archiveName) })
    if (-not $expectedLine) {
        throw "FATAL: Archive '$archiveName' not found in checksums.txt. Aborting."
    }
    $expectedHash = ($expectedLine -split '\s+')[0].ToLower()
    $actualHash   = (Get-FileHash $archivePath -Algorithm SHA256).Hash.ToLower()

    if ($actualHash -ne $expectedHash) {
        throw "FATAL: SHA256 mismatch for ${archiveName}!`n  Expected: $expectedHash`n  Got:      $actualHash`nAborting — do not proceed with a tampered archive."
    }
    Write-Status "✓ SHA256 checksum verified." "Success"

    return $archivePath
}

function Extract-Archive {
    param(
        [string]$ArchivePath,
        [string]$ExtractDir
    )

    try {
        Expand-Archive -Path $ArchivePath -DestinationPath $ExtractDir -Force
        Write-Status "Archive extracted successfully" "Success"
        return $true
    }
    catch {
        Write-Status "Failed to extract archive: $($_.Exception.Message)" "Error"
        return $false
    }
}

function Install-Binary {
    param(
        [string]$ExtractDir,
        [string]$BinDir,
        [string]$InstallName
    )

    $sourcePath = Join-Path $ExtractDir "hangar.exe"
    $targetPath = Join-Path $BinDir "$InstallName.exe"

    # Create bin directory if it doesn't exist
    if (-not (Test-Path $BinDir)) {
        New-Item -ItemType Directory -Path $BinDir -Force | Out-Null
        Write-Status "Created directory: $BinDir" "Info"
    }

    # Remove existing binary if upgrading
    if (Test-Path $targetPath) {
        Remove-Item $targetPath -Force
        Write-Status "Removed existing binary: $targetPath" "Info"
    }

    # Copy new binary
    try {
        Copy-Item $sourcePath $targetPath -Force
        Write-Status "Binary installed to: $targetPath" "Success"
        return $true
    }
    catch {
        Write-Status "Failed to install binary: $($_.Exception.Message)" "Error"
        return $false
    }
}

function Add-ToPath {
    param([string]$BinDir)

    $currentPath = [Environment]::GetEnvironmentVariable("PATH", "User")

    if ($currentPath -notlike "*$BinDir*") {
        $newPath = "$currentPath;$BinDir"
        [Environment]::SetEnvironmentVariable("PATH", $newPath, "User")
        Write-Status "Added $BinDir to PATH" "Success"
        Write-Status "Please restart your terminal or run: `$env:PATH += ';$BinDir'" "Info"
    } else {
        Write-Status "$BinDir is already in PATH" "Info"
    }
}

function Test-Installation {
    param([string]$BinDir, [string]$InstallName)

    $binaryPath = Join-Path $BinDir "$InstallName.exe"

    if (Test-Path $binaryPath) {
        try {
            # Add to current session PATH for testing
            $env:PATH += ";$BinDir"
            $versionOutput = & $binaryPath version 2>&1
            Write-Status "Installation successful!" "Success"
            Write-Status "Version: $versionOutput" "Info"
            return $true
        }
        catch {
            Write-Status "Binary exists but failed to run: $($_.Exception.Message)" "Error"
            return $false
        }
    } else {
        Write-Status "Binary not found at: $binaryPath" "Error"
        return $false
    }
}

# Main installation process
function Main {
    Write-Status "Hangar Windows Installer" "Info"
    Write-Status "=========================" "Info"

    # Check dependencies
    if (-not (Test-Dependencies)) {
        Write-Status "Dependency check failed. Please install missing dependencies and try again." "Error"
        exit 1
    }

    # Get version
    if ($Version -eq "latest") {
        try {
            $Version = Get-LatestVersion
            Write-Status "Latest version: $Version" "Info"
        }
        catch {
            Write-Status "Failed to get latest version. Please specify a version manually." "Error"
            exit 1
        }
    }

    # Detect architecture
    $architecture = Get-Architecture
    Write-Status "Detected architecture: $architecture" "Info"

    # Create temporary directory
    $tempDir = Join-Path $env:TEMP "hangar-install"
    if (Test-Path $tempDir) {
        Remove-Item $tempDir -Recurse -Force
    }
    New-Item -ItemType Directory -Path $tempDir -Force | Out-Null

    try {
        # Download and verify release
        $archivePath = Download-And-Verify-Release -Version $Version -Architecture $architecture -TempDir $tempDir -SkipSig $SkipSignatureCheck.IsPresent

        # Extract archive
        $extractDir = Join-Path $tempDir "extract"
        if (-not (Extract-Archive -ArchivePath $archivePath -ExtractDir $extractDir)) {
            exit 1
        }

        # Install binary
        if (-not (Install-Binary -ExtractDir $extractDir -BinDir $BinDir -InstallName $Name)) {
            exit 1
        }

        # Add to PATH
        Add-ToPath -BinDir $BinDir

        # Test installation
        if (Test-Installation -BinDir $BinDir -InstallName $Name) {
            Write-Status "" "Info"
            Write-Status "Installation completed successfully!" "Success"
            Write-Status "You can now use '$Name' command" "Info"
        } else {
            Write-Status "Installation verification failed" "Error"
            exit 1
        }
    }
    finally {
        # Cleanup
        if (Test-Path $tempDir) {
            Remove-Item $tempDir -Recurse -Force -ErrorAction SilentlyContinue
        }
    }
}

# Run main function
Main
