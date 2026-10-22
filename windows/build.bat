@echo off
REM LimitBar Windows Build Script
echo Building LimitBar for Windows (x64)...
set CGO_ENABLED=0
set GOOS=windows
set GOARCH=amd64
go build -ldflags "-s -w -H=windowsgui" -o LimitBar.exe .
if %ERRORLEVEL% EQU 0 (
    echo Successfully built LimitBar.exe
) else (
    echo Build failed!
)
