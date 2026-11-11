#!/usr/bin/env bash
# Installs or updates the prebuilt pxi.app (latest GitHub release) in /Applications
# and opens it. Nothing else is installed.
#
#   curl -fsSL https://raw.githubusercontent.com/snehal96/pxi/master/macos/get.sh | bash
#
# To build from source instead: macos/install.sh.
set -euo pipefail

URL="https://github.com/snehal96/pxi/releases/latest/download/pxi-mac.zip"
say() { printf '\n\033[1m==> %s\033[0m\n' "$*"; }

[ "$(uname -s)" = Darwin ] || { echo "pxi's app is for macOS."; exit 1; }
major="$(sw_vers -productVersion | cut -d. -f1)"
[ "$major" -ge 13 ] || { echo "pxi needs macOS 13 or later."; exit 1; }

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

say "Downloading pxi"
curl -fL --progress-bar -o "$TMP/pxi-mac.zip" "$URL"
if curl -fsL -o "$TMP/pxi-mac.zip.sha256" "$URL.sha256"; then
  want="$(cut -d' ' -f1 < "$TMP/pxi-mac.zip.sha256")"
  have="$(shasum -a 256 "$TMP/pxi-mac.zip" | cut -d' ' -f1)"
  [ "$want" = "$have" ] || { echo "Checksum mismatch: the download is damaged. Try again."; exit 1; }
fi
ditto -x -k "$TMP/pxi-mac.zip" "$TMP"
[ -d "$TMP/pxi.app" ] || { echo "The download did not contain pxi.app."; exit 1; }

say "Installing into /Applications"
running=0
pgrep -xq pxi && running=1
rm -rf /Applications/pxi.app
ditto "$TMP/pxi.app" /Applications/pxi.app

if [ "$running" = 1 ]; then
  say "Updated. pxi is still running the old version: use pxi > Restart pxi (Shift+Option+Cmd+R)."
else
  open -a /Applications/pxi.app
  say "Done. Run the same command again to update."
fi
