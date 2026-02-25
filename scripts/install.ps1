#!/usr/bin/env pwsh
# Install script for ycy CLI on Windows

$ErrorActionPreference = "Stop"

$Repo = "hackycy-collection/hackycy-cli"
$BinaryName = "ycy.exe"
$InstallDir = "$env:USERPROFILE\.ycy-cli\bin"

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

try {
    Write-Host ""
    Write-Info "Installing ycy CLI..."
    Write-Host ""

    # 1. Get latest version
    Write-Info "Fetching latest version..."
    $Release = Invoke-RestMethod -Uri "https://api.github.com/repos/$Repo/releases/latest" -Headers @{ Accept = "application/vnd.github.v3+json" }
    $Version = $Release.tag_name

    if (-not $Version) {
        Write-Error "Failed to determine the latest version."
    }

    Write-Info "Latest version: $Version"

    # 2. Download binary
    $ArtifactName = "ycy-windows-x64.exe"
    $DownloadUrl = "https://github.com/$Repo/releases/download/$Version/$ArtifactName"

    if (-not (Test-Path $InstallDir)) {
        New-Item -ItemType Directory -Force -Path $InstallDir | Out-Null
    }

    $OutputPath = Join-Path $InstallDir $BinaryName

    Write-Info "Downloading $ArtifactName $Version..."
    Invoke-WebRequest -Uri $DownloadUrl -OutFile $OutputPath -UseBasicParsing

    # Verify download
    $FileSize = (Get-Item $OutputPath).Length
    if ($FileSize -eq 0) {
        Remove-Item $OutputPath -Force
        Write-Error "Downloaded file is empty. Please try again."
    }

    # 3. Add to PATH
    $CurrentPath = [Environment]::GetEnvironmentVariable("PATH", "User")
    if ($CurrentPath -notlike "*$InstallDir*") {
        [Environment]::SetEnvironmentVariable("PATH", "$InstallDir;$CurrentPath", "User")
        Write-Info "Added $InstallDir to your PATH."
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
