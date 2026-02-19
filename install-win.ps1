$ErrorActionPreference = "Stop"

$URL = "https://github.com/hdmain/rawuploader/raw/refs/heads/main/tcpraw.exe"
$InstallPath = "$env:WINDIR\System32\tcpraw.exe"
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
    Write-Host "🔧 Installing tcpraw to System32..."
}

try {
    Copy-Item $TempFile $InstallPath -Force
}
catch {
    Write-Host "❌ Error: Administrator privileges required."
    exit 1
}

Remove-Item $TempFile -Force

Write-Host "✅ Installation / Update completed successfully!"
Write-Host "You can now run: tcpraw"
