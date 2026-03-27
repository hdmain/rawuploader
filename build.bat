@echo off
setlocal

REM Build script for tcpraw:
REM - Windows binary is built natively with Go
REM - Linux binary is built inside WSL
REM Output files are saved in the rawuploader folder as:
REM   tcpraw.exe (Windows) and tcpraw (Linux)

set "ROOT=%~dp0"
set "SRC=%ROOT%src"
set "WIN_OUT=%ROOT%tcpraw.exe"
set "LINUX_OUT_WIN=%ROOT%tcpraw"

if not exist "%SRC%" (
  echo [ERROR] Source folder not found: "%SRC%"
  exit /b 1
)

where go >nul 2>nul
if errorlevel 1 (
  echo [ERROR] Go is not available in PATH on Windows.
  exit /b 1
)

echo [1/2] Building Windows binary...
pushd "%SRC%" >nul
set "GOOS=windows"
set "GOARCH=amd64"
go build -o "%WIN_OUT%" .
if errorlevel 1 (
  popd >nul
  echo [ERROR] Windows build failed.
  exit /b 1
)
popd >nul
set "GOOS="
set "GOARCH="
echo [OK] Windows binary: "%WIN_OUT%"

echo [2/2] Building Linux binary via WSL...

set "ROOT_UNIX=%ROOT:\=/%"
set "ROOT_UNIX=%ROOT_UNIX:C:=/mnt/c%"
set "ROOT_UNIX=%ROOT_UNIX:D:=/mnt/d%"
set "ROOT_UNIX=%ROOT_UNIX:E:=/mnt/e%"
set "ROOT_UNIX=%ROOT_UNIX:F:=/mnt/f%"
set "ROOT_UNIX=%ROOT_UNIX:G:=/mnt/g%"
set "ROOT_UNIX=%ROOT_UNIX:H:=/mnt/h%"
set "ROOT_UNIX=%ROOT_UNIX:I:=/mnt/i%"
set "ROOT_UNIX=%ROOT_UNIX:J:=/mnt/j%"
set "ROOT_UNIX=%ROOT_UNIX:~0,-1%"

wsl bash -lc "cd \"%ROOT_UNIX%/src\" && GOOS=linux GOARCH=amd64 go build -o ../tcpraw ."
if errorlevel 1 (
  echo [ERROR] Linux build via WSL failed.
  exit /b 1
)

echo [OK] Linux binary: "%LINUX_OUT_WIN%"
echo.
echo Build complete.
exit /b 0
