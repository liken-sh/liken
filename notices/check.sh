#!/usr/bin/env bash
# Checks that an image holds the notices of the third-party software in
# it. Each component's smoke check runs this on its image.
#
# Usage: check.sh <image> [<path>...]
#
#   <image>  the full reference of an image in the local Docker daemon
#   <path>   a file that the image must hold, relative to its root, such
#            as usr/share/doc/go/LICENSE for an image with a Go binary
#
# Each list usr/share/doc/liken/<image>.packages names the Debian or
# Ubuntu packages that a closure took files from, and the image must
# hold the copyright file of each package it names. An image with no
# such list and no <path> fails, because the check would prove nothing.
set -euo pipefail

image=$1
shift

work=$(mktemp -d)
id=$(docker create "$image" /none)
trap 'docker rm "$id" >/dev/null; rm -rf "$work"' EXIT

# The file list is written first and read after, because grep -q on a
# pipe closes it early and pipefail would fail the check.
docker export "$id" | tar -t >"$work/files"
mapfile -t lists < <(grep -E '^usr/share/doc/liken/[^/]+\.packages$' "$work/files" || true)
if [ "${#lists[@]}" -eq 0 ] && [ "$#" -eq 0 ]; then
	echo "$image holds no list of packages, and the check names no file" >&2
	exit 1
fi

missing=0
for list in "${lists[@]}"; do
	docker cp "$id:/$list" - | tar -xO >"$work/list"
	count=0
	# The package lines follow the header line that starts with <package>.
	while read -r package _; do
		count=$((count + 1))
		if ! grep -qx "usr/share/doc/$package/copyright" "$work/files"; then
			echo "$image: $list names $package, and usr/share/doc/$package/copyright is not in the image" >&2
			missing=1
		fi
	done < <(sed -n '/^<package>/,$p' "$work/list" | tail -n +2)
	if [ "$count" -eq 0 ]; then
		echo "$image: $list names no package" >&2
		missing=1
	fi
	echo "$list: $count packages"
done
for path in "$@"; do
	if ! grep -qx "$path" "$work/files"; then
		echo "$image: $path is not in the image" >&2
		missing=1
	fi
done
if [ "$missing" -ne 0 ]; then
	exit 1
fi
echo "$image holds its notices"
