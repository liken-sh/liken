#!/usr/bin/env bash
# Proves the media-operator-capabilities image before it ships.
#
# Usage: media-operator-capabilities.sh <image>
#
#   <image>  the full reference of the capabilities image, for example
#            ghcr.io/liken-sh/media-operator-capabilities:2026.10.02-001.
#            The image must be in the local Docker daemon.
#
# The check runs the query against /dev/null, which is not a render
# node. The query calls libva-drm's vaGetDisplayDRM only after the
# dynamic loader, libva.so.2, and libva-drm.so.2 all load, so libva-drm's
# refusal of /dev/null proves the image holds what the query needs. A
# build machine has no GPU to query.
set -euo pipefail

image=$1

# The image must hold the notices of the third-party software in it,
# as notices/check.sh describes.
"$(dirname "$0")/../../notices/check.sh" "$image" \
  usr/share/doc/go/LICENSE

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

if docker run --rm "$image" query /dev/null > "$work/out.txt" 2> "$work/err.txt"; then
	echo "the query of /dev/null succeeded, and it must fail in libva-drm" >&2
	exit 1
fi
if ! grep -q 'vaGetDisplayDRM returned no display for /dev/null' "$work/err.txt"; then
	echo "the query did not reach vaGetDisplayDRM:" >&2
	cat "$work/err.txt" >&2
	exit 1
fi
