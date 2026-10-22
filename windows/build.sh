#!/bin/bash
set -euo pipefail
cd "$(dirname "$0")"

echo "Building LimitBar for Windows (x64 and arm64)..."
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -ldflags "-s -w -H=windowsgui" -o LimitBar.exe .
CGO_ENABLED=0 GOOS=windows GOARCH=arm64 go build -ldflags "-s -w -H=windowsgui" -o LimitBar-arm64.exe .

echo "Built LimitBar.exe (x86_64) and LimitBar-arm64.exe (ARM64)"
