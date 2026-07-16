#!/bin/sh
set -eu
ROOT=$(CDPATH= cd -- "$(dirname "$0")/.." && pwd)
SOURCE_ROOT=$(CDPATH= cd -- "$ROOT/.." && pwd)
OUT=${OUT:-"$ROOT/dist"}
APP="$OUT/OpenConnect Desktop.app"
BUILD_COMMIT=${BUILD_COMMIT:-$(git -C "$SOURCE_ROOT" rev-parse --short=12 HEAD 2>/dev/null || printf unknown)}
DMG="$OUT/OpenConnect-Desktop-$BUILD_COMMIT.dmg"
STAGING="$OUT/.dmg-staging"
test -d "$APP" || "$ROOT/packaging/build-app.sh"
rm -rf "$STAGING"
trap 'rm -rf "$STAGING"' EXIT HUP INT TERM
mkdir -p "$STAGING"
ditto "$APP" "$STAGING/OpenConnect Desktop.app"
ln -s /Applications "$STAGING/Applications"
rm -f "$DMG"
hdiutil create -volname "OpenConnect Desktop" -srcfolder "$STAGING" -ov -format UDZO "$DMG"
printf 'Built %s\n' "$DMG"
