#!/usr/bin/env bash
# Builds macos/dist/pxi.app: the Swift shell plus the pxi server built from this repo,
# both universal (Apple silicon + Intel), ad-hoc signed for local use.
#   macos/build.sh            build
#   macos/build.sh --install  build and copy to /Applications
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
MAC="$ROOT/macos"
OUT="$MAC/dist"
APP="$OUT/pxi.app"
VERSION="$(tr -d ' \r\n' < "$ROOT/VERSION")"
BUILD="$(git -C "$ROOT" rev-list --count HEAD 2>/dev/null || echo 1)"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

echo "==> web assets"
bun "$ROOT/scripts/build-web.js" >/dev/null

echo "==> pxi server (universal)"
for arch in arm64 amd64; do
  (cd "$ROOT" && CGO_ENABLED=0 GOOS=darwin GOARCH="$arch" go build -trimpath -ldflags "-s -w" -o "$TMP/pxi-server-$arch" .)
done
lipo -create -output "$TMP/pxi-server" "$TMP/pxi-server-arm64" "$TMP/pxi-server-amd64"

echo "==> pxi shell (universal)"
for arch in arm64 x86_64; do
  swiftc -O -target "$arch-apple-macos13.0" -o "$TMP/pxi-$arch" "$MAC"/Sources/*.swift
done
lipo -create -output "$TMP/pxi" "$TMP/pxi-arm64" "$TMP/pxi-x86_64"

echo "==> icon"
swift "$MAC/make-icon.swift" "$TMP/AppIcon.iconset"
iconutil -c icns -o "$TMP/AppIcon.icns" "$TMP/AppIcon.iconset"

echo "==> bundle"
rm -rf "$APP"
mkdir -p "$APP/Contents/MacOS" "$APP/Contents/Resources"
cp "$TMP/pxi" "$APP/Contents/MacOS/pxi"
cp "$TMP/pxi-server" "$APP/Contents/Resources/pxi"
cp "$TMP/AppIcon.icns" "$APP/Contents/Resources/AppIcon.icns"
cp "$ROOT/LICENSE" "$APP/Contents/Resources/THIRD-PARTY-LICENSE-px0"
sed -e "s/__VERSION__/$VERSION/" -e "s/__BUILD__/$BUILD/" "$MAC/Info.plist" > "$APP/Contents/Info.plist"
codesign --force --sign - "$APP/Contents/Resources/pxi"
codesign --force --sign - "$APP"

echo "Built $APP ($(du -sh "$APP" | cut -f1))"

if [ "${1:-}" = "--install" ]; then
  rm -rf /Applications/pxi.app
  cp -R "$APP" /Applications/pxi.app
  echo "Installed /Applications/pxi.app"
fi
