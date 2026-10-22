#!/bin/bash
# limitbar installer. Read it before you run it — it is short on purpose.
#
#   bash install.sh                 download the release build, verify it, install to ~/Applications
#   bash install.sh --from-source   build this exact commit yourself instead (needs Command Line Tools)
#   bash install.sh --uninstall     remove the app, its cache and its settings
#
# What it does NOT do: no sudo, no changes outside ~/Applications, ~/Library/Caches/limitbar and (on
# --uninstall) LimitBar's own preferences, no
# Gatekeeper/quarantine overrides, no telemetry, no installing or signing in to any AI CLI.
set -euo pipefail

REPO="w13/limitbar"
VERSION="1.1.0"
# Pinned by this commit of install.sh: the release zip must match it byte for byte, and the source build
# must check out exactly this commit.
ZIP_SHA256="a990edd040dd3cd60d24f5573958e84886090f22f6e8a77a7af3c6b7b408b9c2"
SOURCE_COMMIT="152e68988420c57034ca99e2f8376ad724ece904"

APP_DIR="$HOME/Applications"
APP="$APP_DIR/LimitBar.app"
BUNDLE_ID="io.github.dancik.limitbar"

say() { printf '\033[1m%s\033[0m\n' "$*"; }
die() { printf 'limitbar install: %s\n' "$*" >&2; exit 1; }

[[ "$(uname -s)" == "Darwin" ]] || die "macOS only"
[[ "$EUID" -ne 0 ]] || die "do not run as root — it installs into your own ~/Applications"
major="$(sw_vers -productVersion | cut -d. -f1)"
[[ "$major" -ge 14 ]] || die "needs macOS 14 or later (this is $(sw_vers -productVersion))"

quit_running() { pkill -x -u "$(id -u)" LimitBar 2>/dev/null || true; sleep 0.5; }

if [[ "${1:-}" == "--uninstall" ]]; then
  quit_running
  rm -rf "$APP" "$HOME/Library/Caches/limitbar"
  defaults delete "$BUNDLE_ID" 2>/dev/null || true
  say "limitbar removed. If you turned on Launch at Login, macOS drops that entry by itself."
  exit 0
fi

STAGE="$(mktemp -d)"
trap 'rm -rf "$STAGE"' EXIT

if [[ "${1:-}" == "--from-source" ]]; then
  xcrun --find swift >/dev/null 2>&1 || die "Swift not found. Install Command Line Tools first: xcode-select --install"
  say "Fetching source commit $SOURCE_COMMIT"
  git -C "$STAGE" init -q src
  git -C "$STAGE/src" fetch -q --depth 1 "https://github.com/$REPO.git" "$SOURCE_COMMIT"
  git -C "$STAGE/src" -c advice.detachedHead=false checkout -q FETCH_HEAD
  [[ "$(git -C "$STAGE/src" rev-parse HEAD)" == "$SOURCE_COMMIT" ]] || die "checked-out commit does not match the pin"
  say "Building (about 2 minutes)"
  "$STAGE/src/scripts/build-app.sh" "$STAGE/out" >/dev/null
  NEW="$STAGE/out/LimitBar.app"
else
  URL="https://github.com/$REPO/releases/download/v$VERSION/LimitBar-$VERSION.zip"
  say "Downloading LimitBar $VERSION"
  curl -fsSL --proto '=https' --tlsv1.2 -o "$STAGE/LimitBar.zip" "$URL"
  got="$(shasum -a 256 "$STAGE/LimitBar.zip" | cut -d' ' -f1)"
  [[ "$got" == "$ZIP_SHA256" ]] || die "SHA-256 mismatch: got $got, expected $ZIP_SHA256 — not installing"
  say "SHA-256 verified"
  # Checked whenever the GitHub CLI can do it (installed and signed in); the pinned SHA-256 above is
  # always checked and is what guarantees these are the published bytes.
  if command -v gh >/dev/null 2>&1 && gh auth status --hostname github.com >/dev/null 2>&1; then
    gh attestation verify "$STAGE/LimitBar.zip" --repo "$REPO" \
      --signer-workflow "$REPO/.github/workflows/release.yml" --deny-self-hosted-runners >/dev/null \
      || die "build provenance check failed — not installing"
    say "Build provenance verified (built by this repo's release workflow on GitHub-hosted runners)"
  else
    echo "(GitHub CLI not installed or not signed in: build provenance not checked; the pinned SHA-256 above was.)"
  fi
  # Refuse archives with absolute paths, '..' or symlinks before extracting anything.
  if zipinfo -1 "$STAGE/LimitBar.zip" | grep -Eq '(^/|(^|/)\.\.(/|$))'; then die "unsafe path in archive"; fi
  if zipinfo -l "$STAGE/LimitBar.zip" | grep -q '^l'; then die "symlink in archive"; fi
  mkdir "$STAGE/out"
  ditto -x -k "$STAGE/LimitBar.zip" "$STAGE/out"
  NEW="$STAGE/out/LimitBar.app"
fi

[[ -d "$NEW" ]] || die "no LimitBar.app produced"
[[ -z "$(find "$NEW" -type l)" ]] || die "symlink inside the app bundle"
[[ "$(/usr/libexec/PlistBuddy -c 'Print :CFBundleIdentifier' "$NEW/Contents/Info.plist")" == "$BUNDLE_ID" ]] || die "unexpected bundle id"
[[ "$(/usr/libexec/PlistBuddy -c 'Print :CFBundleShortVersionString' "$NEW/Contents/Info.plist")" == "$VERSION" ]] || die "unexpected version"
codesign --verify --strict "$NEW" || die "code signature does not verify — bundle was modified"

quit_running
mkdir -p "$APP_DIR"
if [[ -e "$APP" ]]; then mv "$APP" "$STAGE/previous.app"; fi
mv "$NEW" "$APP"
say "Installed $APP"
open "$APP"
cat <<'MSG'

LimitBar is in your menu bar. It shows every AI CLI you have installed and signed in to:
  Claude Code (claude) · Google Antigravity (agy) · Codex (codex) · Grok Build (grok)
Hover it for 5-hour and weekly details. Not signed in to one? Sign in with that CLI itself —
LimitBar never asks for, reads or stores your credentials.
MSG
