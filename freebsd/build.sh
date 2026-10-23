#!/bin/bash
set -euo pipefail
cd "$(dirname "$0")"

echo "Building LimitBar for FreeBSD (amd64 and arm64)..."
CGO_ENABLED=0 GOOS=freebsd GOARCH=amd64 go build -ldflags "-s -w" -o limitbar-freebsd-amd64 .
CGO_ENABLED=0 GOOS=freebsd GOARCH=arm64 go build -ldflags "-s -w" -o limitbar-freebsd-arm64 .

echo "Built limitbar-freebsd-amd64 (x86_64) and limitbar-freebsd-arm64 (ARM64)"
