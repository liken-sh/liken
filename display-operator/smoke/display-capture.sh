#!/usr/bin/env bash
# Runs the capture sidecar image's encoder once before it ships.
#
# Usage: display-capture.sh <image>
#
#   <image>  the full reference of the display-capture image, for
#            example ghcr.io/liken-sh/display-capture:2026.10.02-001.
#            The image must be in the local Docker daemon.
#
# The capture sidecar starts one ffmpeg process per request by absolute
# path, so a run proves that path is in the image, the way the weston,
# ffmpeg, and mpv checks prove each of those images.
set -euo pipefail

image=$1

# The image must hold the notices of the third-party software in it,
# as notices/check.sh describes.
"$(dirname "$0")/../../notices/check.sh" "$image" \
  usr/share/doc/go/LICENSE

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

docker run --rm --entrypoint /usr/bin/ffmpeg \
  "$image" -version > "$work/capture-ffmpeg.txt"
grep -q '^ffmpeg version ' "$work/capture-ffmpeg.txt"
