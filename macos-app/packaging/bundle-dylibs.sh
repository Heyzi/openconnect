#!/bin/sh
set -eu

test "$#" -eq 3 || { echo "usage: $0 OPENCONNECT LIBOPENCONNECT FRAMEWORKS" >&2; exit 64; }
OPENCONNECT=$1
LIBOPENCONNECT=$2
FRAMEWORKS=$3
mkdir -p "$FRAMEWORKS"

bundle_dependencies() {
	local source_file=$1
	local target_file=$2
	local loader_prefix=$3
	local is_framework=$4
	local dependency base bundled

	otool -L "$source_file" | awk 'NR > 1 { print $1 }' | while IFS= read -r dependency; do
		case "$dependency" in
			/opt/homebrew/*|/usr/local/*)
				base=$(basename "$dependency")
				bundled="$FRAMEWORKS/$base"
				if [ ! -e "$bundled" ]; then
					cp -L "$dependency" "$bundled"
					chmod 0755 "$bundled"
					bundle_dependencies "$dependency" "$bundled" '@loader_path/' yes
				fi
				install_name_tool -change "$dependency" "$loader_prefix$base" "$target_file"
				;;
		esac
	done
	if [ "$is_framework" = yes ]; then
		install_name_tool -id "@loader_path/$(basename "$target_file")" "$target_file"
	fi
}

bundle_dependencies "$OPENCONNECT" "$OPENCONNECT" '@loader_path/../../Frameworks/' no
bundle_dependencies "$LIBOPENCONNECT" "$LIBOPENCONNECT" '@loader_path/../../Frameworks/' no
