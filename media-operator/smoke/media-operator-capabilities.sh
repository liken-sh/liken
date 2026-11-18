#!/usr/bin/env bash
# Checks the media-operator-capabilities image before it is published.
#
# Usage: media-operator-capabilities.sh <image>
#
#   <image>  the full reference of the capabilities image, for example
#            ghcr.io/liken-sh/media-operator-capabilities:2026.10.02-001.
#            The image must be in the local Docker daemon.
#
# The CI runner has no GPU, so the check runs the query against
# /dev/null, which is not a render node. The query calls vaGetDisplayDRM
# only after the dynamic loader has started the program and libva.so.2
# and libva-drm.so.2 have loaded. So when vaGetDisplayDRM refuses
# /dev/null, those libraries are in the image. The check does not load
# the media driver, which libva opens later, in vaInitialize.
set -euo pipefail

image=$1

# Checks the license notices of the third-party software in the image
# (notices/check.sh).
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
