$ErrorActionPreference = "Stop"

$URL = "https://github.com/hdmain/rawuploader/raw/refs/heads/main/tcpraw.exe"
$InstallPath = "$env:ProgramFiles\tcpraw\tcpraw.exe"
$TempFile = New-TemporaryFile

Write-Host "📥 Downloading latest tcpraw..."

try {
    Invoke-WebRequest -Uri $URL -OutFile $TempFile
}
catch {
    Write-Host "❌ Error: Download failed."
    exit 1
}

if ((Get-Item $TempFile).Length -eq 0) {
    Write-Host "❌ Error: Downloaded file is empty."
    exit 1
}

if (Test-Path $InstallPath) {
    Write-Host "🔄 Updating existing installation..."
}
else {
    Write-Host "🔧 Installing tcpraw..."
    New-Item -ItemType Directory -Force -Path (Split-Path $InstallPath) | Out-Null
}

Copy-Item $TempFile $InstallPath -Force

Remove-Item $TempFile -Force

Write-Host "✅ Installation / Update completed successfully!"
Write-Host "You can run the program using: $InstallPath"
