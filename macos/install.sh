#!/usr/bin/env bash
# Installs or updates pxi on a Mac in one step: fetches what the build needs
# (Xcode command line tools, Homebrew, Go, Bun), gets the source, builds
# pxi.app into /Applications and opens it. Safe to run again to update.
#
#   bash -c "$(curl -fsSL https://raw.githubusercontent.com/snehal96/pxi/master/macos/install.sh)"
#
# Or from a clone: macos/install.sh
set -euo pipefail

REPO="https://github.com/snehal96/pxi.git"
DEST="${PXI_SRC:-$HOME/pxi}"
say() { printf '\n\033[1m==> %s\033[0m\n' "$*"; }

[ "$(uname -s)" = Darwin ] || { echo "pxi's app is for macOS."; exit 1; }

# 1. Xcode command line tools (Swift, git). macOS shows its own installer.
if ! xcode-select -p >/dev/null 2>&1; then
  say "Installing the Xcode command line tools (click Install in the window that opens)"
  xcode-select --install >/dev/null 2>&1 || true
  until xcode-select -p >/dev/null 2>&1; do sleep 5; done
fi

# 2. Homebrew, to install Go and Bun.
if ! command -v brew >/dev/null 2>&1; then
  for b in /opt/homebrew/bin/brew /usr/local/bin/brew; do [ -x "$b" ] && eval "$("$b" shellenv)"; done
fi
if ! command -v brew >/dev/null 2>&1; then
  say "Installing Homebrew (it asks for your Mac password)"
  /bin/bash -c "$(curl -fsSL https://raw.githubusercontent.com/Homebrew/install/HEAD/install.sh)" </dev/tty
  for b in /opt/homebrew/bin/brew /usr/local/bin/brew; do [ -x "$b" ] && eval "$("$b" shellenv)"; done
fi

# 3. Go and Bun.
command -v go  >/dev/null 2>&1 || { say "Installing Go";  brew install go; }
command -v bun >/dev/null 2>&1 || { say "Installing Bun"; brew install oven-sh/bun/bun; }

# 4. The source: this clone if run from one, otherwise ~/pxi (cloned or updated).
# (Piped through curl there is no script file, so it never guesses from the current folder.)
SELF="${BASH_SOURCE[0]:-}"
HERE=""
[ -n "$SELF" ] && [ -f "$SELF" ] && HERE="$(cd "$(dirname "$SELF")" && pwd)"
if [ -n "$HERE" ] && [ -f "$HERE/../go.mod" ] && [ -x "$HERE/build.sh" ]; then
  SRC="$(cd "$HERE/.." && pwd)"
elif [ -d "$DEST/.git" ]; then
  say "Updating $DEST"
  git -C "$DEST" pull --ff-only
  SRC="$DEST"
else
  say "Downloading pxi into $DEST"
  git clone "$REPO" "$DEST"
  SRC="$DEST"
fi

# 5. Build, install, open.
say "Building pxi (1-2 minutes the first time)"
"$SRC/macos/build.sh" --install
pgrep -xq pxi || open -a /Applications/pxi.app
say "Done. To update later, run the same command again."
