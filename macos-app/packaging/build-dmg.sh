#!/bin/sh
set -eu
ROOT=$(CDPATH= cd -- "$(dirname "$0")/.." && pwd); OUT=${OUT:-"$ROOT/dist"}; APP="$OUT/OpenConnect Desktop.app"; DMG="$OUT/OpenConnect-Desktop.dmg"
test -d "$APP" || "$ROOT/packaging/build-app.sh"
rm -f "$DMG"; hdiutil create -volname "OpenConnect Desktop" -srcfolder "$APP" -ov -format UDZO "$DMG"; printf 'Built %s\n' "$DMG"
