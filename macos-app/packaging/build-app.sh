#!/bin/sh
set -eu
ROOT=$(CDPATH= cd -- "$(dirname "$0")/.." && pwd)
OUT=${OUT:-"$ROOT/dist"}; APP="$OUT/OpenConnect Desktop.app"; CONTENTS="$APP/Contents"
TRAY_APP="$CONTENTS/Resources/OpenConnect Tray.app"; TRAY_CONTENTS="$TRAY_APP/Contents"
GO=${GO:-go}
GOCACHE=${GOCACHE:-/tmp/openconnect-desktop-go-cache}
export GOCACHE
SWIFT_MODULECACHE_PATH=${SWIFT_MODULECACHE_PATH:-/tmp/openconnect-desktop-swift-cache}
export SWIFT_MODULECACHE_PATH
rm -rf "$APP"; mkdir -p "$CONTENTS/MacOS" "$CONTENTS/Resources/bin" "$TRAY_CONTENTS/MacOS"
cp "$ROOT/packaging/Info.plist" "$CONTENTS/Info.plist"
cp "$ROOT/packaging/Tray-Info.plist" "$TRAY_CONTENTS/Info.plist"
CGO_ENABLED=0 "$GO" build -trimpath -ldflags='-s -w' -o "$CONTENTS/MacOS/openconnect-desktop" "$ROOT/cmd/openconnect-desktop"
CGO_ENABLED=0 "$GO" build -trimpath -ldflags='-s -w' -o "$CONTENTS/Resources/bin/openconnect-helper" "$ROOT/cmd/openconnect-helper"
CGO_ENABLED=0 "$GO" build -trimpath -ldflags='-s -w' -o "$CONTENTS/Resources/bin/openconnect-script-hook" "$ROOT/cmd/openconnect-script-hook"
xcrun swiftc -O -framework AppKit -o "$TRAY_CONTENTS/MacOS/openconnect-tray" "$ROOT/native/macos/Tray.swift"
xcrun swiftc -O -framework Security -o "$CONTENTS/MacOS/openconnect-keychain" "$ROOT/native/macos/Keychain.swift"
if [ -n "${OPENCONNECT_BINARY:-}" ]; then cp "$OPENCONNECT_BINARY" "$CONTENTS/Resources/bin/openconnect"; fi
if [ -n "${OPENCONNECT_LIBRARY:-}" ]; then
  cp "$OPENCONNECT_LIBRARY" "$CONTENTS/Resources/bin/libopenconnect.5.dylib"
  install_name_tool -change /usr/local/lib/libopenconnect.5.dylib @loader_path/libopenconnect.5.dylib "$CONTENTS/Resources/bin/openconnect"
  install_name_tool -id @loader_path/libopenconnect.5.dylib "$CONTENTS/Resources/bin/libopenconnect.5.dylib"
fi
if [ -n "${VPNC_SCRIPT:-}" ]; then cp "$VPNC_SCRIPT" "$CONTENTS/Resources/vpnc-script"; fi
printf 'Built %s\n' "$APP"
