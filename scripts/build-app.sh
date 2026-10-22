#!/bin/bash
# Build LimitBar.app from this checkout. Used by install.sh --from-source and by the release workflow.
#   scripts/build-app.sh <out-dir> [--universal]
# Produces <out-dir>/LimitBar.app, ad-hoc signed. Needs Command Line Tools (or Xcode for --universal).
set -euo pipefail
cd "$(dirname "$0")/.."
OUT="${1:?usage: build-app.sh <out-dir> [--universal]}"
VERSION="$(cat VERSION)"

if [[ "${2:-}" == "--universal" ]]; then
  ARM_DIR="$(mktemp -d)"
  X86_DIR="$(mktemp -d)"
  swift build -c release --arch arm64
  cp "$(swift build -c release --arch arm64 --show-bin-path)/LimitBar" "$ARM_DIR/LimitBar"
  swift build -c release --arch x86_64
  cp "$(swift build -c release --arch x86_64 --show-bin-path)/LimitBar" "$X86_DIR/LimitBar"
  BIN="$(mktemp -d)/LimitBar"
  lipo -create -output "$BIN" "$ARM_DIR/LimitBar" "$X86_DIR/LimitBar"
  lipo -info "$BIN"
  rm -rf "$ARM_DIR" "$X86_DIR"
else
  swift build -c release
  BIN="$(swift build -c release --show-bin-path)/LimitBar"
fi

APP="$OUT/LimitBar.app"
rm -rf "$APP"
mkdir -p "$APP/Contents/MacOS" "$APP/Contents/Resources/marks"
cp "$BIN" "$APP/Contents/MacOS/LimitBar"
cp Resources/marks/*.svg "$APP/Contents/Resources/marks/"
cp Resources/cli.sb "$APP/Contents/Resources/cli.sb"
cat > "$APP/Contents/Info.plist" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
  <key>CFBundleIdentifier</key><string>io.github.dancik.limitbar</string>
  <key>CFBundleName</key><string>LimitBar</string>
  <key>CFBundleDisplayName</key><string>LimitBar</string>
  <key>CFBundleExecutable</key><string>LimitBar</string>
  <key>CFBundlePackageType</key><string>APPL</string>
  <key>CFBundleShortVersionString</key><string>$VERSION</string>
  <key>CFBundleVersion</key><string>$VERSION</string>
  <key>LSMinimumSystemVersion</key><string>14.0</string>
  <key>LSUIElement</key><true/>
  <key>NSHighResolutionCapable</key><true/>
</dict></plist>
PLIST
# Ad-hoc signature: no Apple Developer ID (see README → Security). It still seals the bundle, so
# `codesign --verify --strict` detects any byte changed after the build.
codesign --force --sign - --timestamp=none "$APP"
codesign --verify --strict "$APP"
echo "built $APP ($VERSION)"
