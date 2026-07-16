#!/bin/sh
set -eu
ROOT=$(CDPATH= cd -- "$(dirname "$0")/.." && pwd)
SOURCE_ROOT=$(CDPATH= cd -- "$ROOT/.." && pwd)
OUT=${OUT:-"$ROOT/dist"}; APP="$OUT/OpenConnect Desktop.app"; CONTENTS="$APP/Contents"
TRAY_APP="$CONTENTS/Resources/OpenConnect Tray.app"; TRAY_CONTENTS="$TRAY_APP/Contents"
CODESIGN_IDENTITY=${CODESIGN_IDENTITY:--}
OPENCONNECT_BINARY=${OPENCONNECT_BINARY:-"$SOURCE_ROOT/.libs/openconnect"}
OPENCONNECT_LIBRARY=${OPENCONNECT_LIBRARY:-"$SOURCE_ROOT/.libs/libopenconnect.5.dylib"}
VPNC_SCRIPT=${VPNC_SCRIPT:-/opt/homebrew/etc/vpnc/vpnc-script}
GO=${GO:-go}
BUILD_COMMIT=${BUILD_COMMIT:-$(git -C "$SOURCE_ROOT" rev-parse --short=12 HEAD 2>/dev/null || printf unknown)}
GOCACHE=${GOCACHE:-/tmp/openconnect-desktop-go-cache}
export GOCACHE
SWIFT_MODULECACHE_PATH=${SWIFT_MODULECACHE_PATH:-/tmp/openconnect-desktop-swift-cache}
export SWIFT_MODULECACHE_PATH
CLANG_MODULE_CACHE_PATH=${CLANG_MODULE_CACHE_PATH:-/tmp/openconnect-desktop-clang-cache}
export CLANG_MODULE_CACHE_PATH
rm -rf "$APP"; mkdir -p "$CONTENTS/MacOS" "$CONTENTS/Resources/bin" "$TRAY_CONTENTS/MacOS"
cp "$ROOT/packaging/Info.plist" "$CONTENTS/Info.plist"
cp "$ROOT/packaging/Tray-Info.plist" "$TRAY_CONTENTS/Info.plist"
if [ -f "$ROOT/assets/AppIcon.icns" ]; then cp "$ROOT/assets/AppIcon.icns" "$CONTENTS/Resources/AppIcon.icns"; fi
CGO_ENABLED=0 "$GO" build -trimpath -ldflags="-s -w -X main.buildCommit=$BUILD_COMMIT" -o "$CONTENTS/MacOS/openconnect-desktop" "$ROOT/cmd/openconnect-desktop"
CGO_ENABLED=0 "$GO" build -trimpath -ldflags='-s -w' -o "$CONTENTS/Resources/bin/openconnect-helper" "$ROOT/cmd/openconnect-helper"
CGO_ENABLED=0 "$GO" build -trimpath -ldflags='-s -w' -o "$CONTENTS/Resources/bin/openconnect-script-hook" "$ROOT/cmd/openconnect-script-hook"
xcrun swiftc -O -framework AppKit -o "$TRAY_CONTENTS/MacOS/openconnect-tray" "$ROOT/native/macos/Tray.swift"
xcrun swiftc -O -framework Security -o "$CONTENTS/MacOS/openconnect-keychain" "$ROOT/native/macos/Keychain.swift"
test -x "$OPENCONNECT_BINARY" || { echo "OpenConnect binary not found: $OPENCONNECT_BINARY" >&2; exit 1; }
test -f "$OPENCONNECT_LIBRARY" || { echo "OpenConnect library not found: $OPENCONNECT_LIBRARY" >&2; exit 1; }
test -f "$VPNC_SCRIPT" || { echo "vpnc-script not found: $VPNC_SCRIPT" >&2; exit 1; }
cp "$OPENCONNECT_BINARY" "$CONTENTS/Resources/bin/openconnect"
cp "$OPENCONNECT_LIBRARY" "$CONTENTS/Resources/bin/libopenconnect.5.dylib"
cp "$VPNC_SCRIPT" "$CONTENTS/Resources/vpnc-script"
chmod 0755 "$CONTENTS/Resources/bin/openconnect" "$CONTENTS/Resources/vpnc-script"
install_name_tool -change /usr/local/lib/libopenconnect.5.dylib @loader_path/libopenconnect.5.dylib "$CONTENTS/Resources/bin/openconnect"
install_name_tool -id @loader_path/libopenconnect.5.dylib "$CONTENTS/Resources/bin/libopenconnect.5.dylib"
mkdir -p "$CONTENTS/Frameworks" "$CONTENTS/Resources/Licenses"
"$ROOT/packaging/bundle-dylibs.sh" "$CONTENTS/Resources/bin/openconnect" "$CONTENTS/Resources/bin/libopenconnect.5.dylib" "$CONTENTS/Frameworks"
cp "$SOURCE_ROOT/COPYING.LGPL" "$CONTENTS/Resources/Licenses/OpenConnect-LGPL-2.1.txt"
find "$CONTENTS/Frameworks" -type f -exec codesign --force --timestamp=none --options runtime --sign "$CODESIGN_IDENTITY" {} \;
codesign --force --timestamp=none --options runtime --sign "$CODESIGN_IDENTITY" "$CONTENTS/Resources/bin/libopenconnect.5.dylib"
codesign --force --timestamp=none --options runtime --entitlements "$ROOT/packaging/OpenConnect.entitlements" --sign "$CODESIGN_IDENTITY" "$CONTENTS/Resources/bin/openconnect"
codesign --force --timestamp=none --options runtime --sign "$CODESIGN_IDENTITY" "$CONTENTS/Resources/bin/openconnect-helper"
codesign --force --timestamp=none --options runtime --sign "$CODESIGN_IDENTITY" "$CONTENTS/Resources/bin/openconnect-script-hook"
codesign --force --timestamp=none --options runtime --sign "$CODESIGN_IDENTITY" "$CONTENTS/MacOS/openconnect-keychain"
codesign --force --timestamp=none --options runtime --sign "$CODESIGN_IDENTITY" "$TRAY_APP"
codesign --force --timestamp=none --options runtime --sign "$CODESIGN_IDENTITY" "$CONTENTS/MacOS/openconnect-desktop"
codesign --force --timestamp=none --options runtime --sign "$CODESIGN_IDENTITY" "$APP"
printf 'Built %s\n' "$APP"
