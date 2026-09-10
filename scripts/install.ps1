#!/usr/bin/env pwsh
# Install script for ycy CLI on Windows

$ErrorActionPreference = "Stop"

$Repo = if ($env:YCY_INSTALL_REPO) { $env:YCY_INSTALL_REPO } else { "hackycy/hackycy-cli" }
$BinaryName = if ($env:YCY_INSTALL_BINARY_NAME) { $env:YCY_INSTALL_BINARY_NAME } else { "ycy.exe" }
$InstallDir = if ($env:YCY_INSTALL_DIR) { $env:YCY_INSTALL_DIR } else { "$env:USERPROFILE\.ycy-cli\bin" }
$ChecksumsFile = "SHA256SUMS"
$ApiUrl = if ($env:YCY_INSTALL_API_URL) { $env:YCY_INSTALL_API_URL } else { "https://api.github.com/repos/$Repo/releases/latest" }
$DownloadBaseUrl = if ($env:YCY_INSTALL_DOWNLOAD_BASE) { $env:YCY_INSTALL_DOWNLOAD_BASE } else { "https://github.com/$Repo/releases/download" }

function Write-Info {
    param([string]$Message)
    Write-Host $Message -ForegroundColor Cyan
}

function Write-Success {
    param([string]$Message)
    Write-Host $Message -ForegroundColor Green
}

function Write-Error {
    param([string]$Message)
    Write-Host "error: $Message" -ForegroundColor Red
    exit 1
}

function Get-ExpectedHash {
    param(
        [string]$ChecksumsContent,
        [string]$ArtifactName
    )

    $escapedArtifactName = [regex]::Escape($ArtifactName)
    foreach ($line in ($ChecksumsContent -split "`r?`n")) {
        $trimmed = $line.Trim()
        if ($trimmed -match "^(?<hash>[A-Fa-f0-9]{64})\s+\*?(?<name>.+)$" -and $matches.name -eq $ArtifactName) {
            return $matches.hash.ToLowerInvariant()
        }
    }

    Write-Error "Failed to find checksum for $ArtifactName."
}

function Assert-FileHash {
    param(
        [string]$Path,
        [string]$ExpectedHash
    )

    $actualHash = (Get-FileHash -Path $Path -Algorithm SHA256).Hash.ToLowerInvariant()
    if ($actualHash -ne $ExpectedHash) {
        throw "Checksum verification failed for $(Split-Path $Path -Leaf)."
    }
}

function Assert-BinaryVersion {
    param(
        [string]$Path,
        [string]$ExpectedVersion
    )

    $actualVersion = (& $Path --version 2>$null | Out-String).Trim()
    if ($LASTEXITCODE -ne 0) {
        throw "Installed binary failed to execute self-check."
    }

    if ($actualVersion -ne $ExpectedVersion -and $actualVersion -notlike "ycy/$ExpectedVersion*") {
        throw "Installed binary reported unexpected version: $actualVersion"
    }
}

$TempPath = $null

try {
    Write-Host ""
    Write-Info "Installing ycy CLI..."
    Write-Host ""

    # 1. Get latest version
    Write-Info "Fetching latest version..."
    $Release = Invoke-RestMethod -Uri $ApiUrl -Headers @{ Accept = "application/vnd.github.v3+json" }
    $Version = $Release.tag_name

    if (-not $Version) {
        Write-Error "Failed to determine the latest version."
    }

    Write-Info "Latest version: $Version"

    # 2. Download binary
    $architecture = if ($env:YCY_INSTALL_ARCH) { $env:YCY_INSTALL_ARCH } else { [System.Runtime.InteropServices.RuntimeInformation]::OSArchitecture.ToString() }
    switch ($architecture.ToLowerInvariant()) {
        "x64" { $publicArchitecture = "x64" }
        "amd64" { $publicArchitecture = "x64" }
        "arm64" { $publicArchitecture = "arm64" }
        "aarch64" { $publicArchitecture = "arm64" }
        default { Write-Error "Unsupported architecture: $architecture" }
    }
    $ArtifactName = "ycy-windows-$publicArchitecture.exe"
    $DownloadUrl = "$DownloadBaseUrl/$Version/$ArtifactName"
    $Asset = $Release.assets | Where-Object { $_.name -eq $ArtifactName } | Select-Object -First 1
    if (-not $Asset) {
        Write-Error "Failed to find release asset metadata for $ArtifactName."
    }

    $ExpectedHash = $null
    if ($Asset.digest -and $Asset.digest.StartsWith('sha256:')) {
        $ExpectedHash = $Asset.digest.Substring(7).ToLowerInvariant()
    }
    else {
        $ChecksumsUrl = "$DownloadBaseUrl/$Version/$ChecksumsFile"
        $ChecksumsContent = (Invoke-WebRequest -Uri $ChecksumsUrl -UseBasicParsing).Content
        $ExpectedHash = Get-ExpectedHash -ChecksumsContent $ChecksumsContent -ArtifactName $ArtifactName
    }

    if (-not (Test-Path $InstallDir)) {
        New-Item -ItemType Directory -Force -Path $InstallDir | Out-Null
    }

    $OutputPath = Join-Path $InstallDir $BinaryName
    $TempPath = "$OutputPath.tmp.$PID"
    $BackupPath = "$OutputPath.backup.$PID"
    $ExpectedVersion = $Version.TrimStart('v')

    Write-Info "Downloading $ArtifactName $Version..."
    if (Test-Path $TempPath) {
        Remove-Item $TempPath -Force
    }
    Invoke-WebRequest -Uri $DownloadUrl -OutFile $TempPath -UseBasicParsing

    # Verify download
    $FileSize = (Get-Item $TempPath).Length
    if ($FileSize -eq 0) {
        Remove-Item $TempPath -Force
        Write-Error "Downloaded file is empty. Please try again."
    }

    Assert-FileHash -Path $TempPath -ExpectedHash $ExpectedHash

    try {
        Unblock-File -Path $TempPath -ErrorAction Stop
    }
    catch {
        # Ignore if the file is not blocked or the platform does not support MOTW.
    }

    $HadBackup = $false
    try {
        if (Test-Path $OutputPath) {
            if (Test-Path $BackupPath) {
                Remove-Item $BackupPath -Force
            }
            Move-Item -Path $OutputPath -Destination $BackupPath -Force
            $HadBackup = $true
        }

        Move-Item -Path $TempPath -Destination $OutputPath -Force
        $TempPath = $null

        Assert-FileHash -Path $OutputPath -ExpectedHash $ExpectedHash
        Assert-BinaryVersion -Path $OutputPath -ExpectedVersion $ExpectedVersion

        if ($HadBackup -and (Test-Path $BackupPath)) {
            Remove-Item $BackupPath -Force
        }
    }
    catch {
        if (Test-Path $OutputPath) {
            Remove-Item $OutputPath -Force
        }
        if ($HadBackup -and (Test-Path $BackupPath)) {
            Move-Item -Path $BackupPath -Destination $OutputPath -Force
        }
        if ($TempPath -and (Test-Path -LiteralPath $TempPath)) {
            Remove-Item -LiteralPath $TempPath -Force
        }
        throw
    }

    # 3. Add to PATH
    if ($env:YCY_INSTALL_SKIP_PATH -ne "1") {
        $CurrentPath = [Environment]::GetEnvironmentVariable("PATH", "User")
        if ($CurrentPath -notlike "*$InstallDir*") {
            [Environment]::SetEnvironmentVariable("PATH", "$InstallDir;$CurrentPath", "User")
            Write-Info "Added $InstallDir to your PATH."
        }
    }

    # 4. Success message
    Write-Host ""
    Write-Success "ycy $Version has been installed successfully!"
    Write-Host ""
    Write-Host "  Install path: $OutputPath"
    Write-Host ""
    Write-Host "  To get started, open a new terminal and run:"
    Write-Host ""
    Write-Host "    ycy --help"
    Write-Host ""
}
catch {
    Write-Host ""
    Write-Error "Installation failed: $_"
}
finally {
    if ($TempPath -and (Test-Path -LiteralPath $TempPath)) {
        Remove-Item -LiteralPath $TempPath -Force -ErrorAction SilentlyContinue
    }
}
