#!/bin/sh
set -eu

ROOT=$(CDPATH= cd -- "$(dirname "$0")/.." && pwd)
SOURCE_ROOT=$(CDPATH= cd -- "$ROOT/.." && pwd)
BUILD_COMMIT=$(git -C "$SOURCE_ROOT" rev-parse --short=12 HEAD 2>/dev/null || printf unknown)
test -z "$(git -C "$SOURCE_ROOT" status --porcelain 2>/dev/null)" || BUILD_COMMIT="$BUILD_COMMIT-dirty"
export BUILD_COMMIT
GOCACHE=${GOCACHE:-/tmp/openconnect-desktop-go-cache}
SWIFT_MODULECACHE_PATH=${SWIFT_MODULECACHE_PATH:-/tmp/openconnect-desktop-swift-cache}
CLANG_MODULE_CACHE_PATH=${CLANG_MODULE_CACHE_PATH:-/tmp/openconnect-desktop-clang-cache}
export GOCACHE SWIFT_MODULECACHE_PATH CLANG_MODULE_CACHE_PATH

cd "$SOURCE_ROOT"
test -f Makefile || ./configure LDFLAGS="-framework CoreFoundation -framework SystemConfiguration -framework Security"
make clean
make
cd "$ROOT"
go test ./...
"$ROOT/packaging/build-app.sh"
SKIP_APP_BUILD=1 "$ROOT/packaging/build-dmg.sh"

DMG="$ROOT/dist/OpenConnect-Desktop-$BUILD_COMMIT.dmg"
codesign --verify --deep --strict "$ROOT/dist/OpenConnect Desktop.app"
hdiutil verify "$DMG"
shasum -a 256 "$DMG" > "$DMG.sha256"
printf 'Release ready: %s\n' "$DMG"
