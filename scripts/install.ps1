$Version = if ($args[0]) { $args[0] } else { "latest" }
$BinaryName = "revtunnel.exe"
$InstallDir = "$env:LOCALAPPDATA\Programs\revtunnel"
$BaseUrl = "https://github.com/oluu-web/revtunnel/releases/download/$Version"
$Filename = "revtunnel-windows-amd64.exe"
$Url = "$BaseUrl/$Filename"

New-Item -ItemType Directory -Force -Path $InstallDir | Out-Null

Write-Host "Downloading $Filename..."
Invoke-WebRequest -Uri $Url -OutFile "$InstallDir\$BinaryName"

$CurrentPath = [Environment]::GetEnvironmentVariable("PATH", "User")
if ($CurrentPath -notlike "*$InstallDir*") {
    [Environment]::SetEnvironmentVariable("PATH", "$CurrentPath;$InstallDir", "User")
    Write-Host "Added $InstallDir to PATH"
}

Write-Host "✓ Installed successfully!"
Write-Host "  Restart your terminal, then run: revtunnel login --api-key <your-key>"