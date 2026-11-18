#!/usr/bin/env bash
# Checks the display-capture image before CI publishes it. The capture
# container runs /usr/bin/ffmpeg by that absolute path, so the check
# fails when ffmpeg is not there or does not start.
#
# Usage: display-capture.sh <image>
#
#   <image>  the full reference of the display-capture image in the
#            local Docker daemon, for example
#            ghcr.io/liken-sh/display-capture:2026.10.02-001
set -euo pipefail

image=$1

"$(dirname "$0")/../../notices/check.sh" "$image" \
  usr/share/doc/go/LICENSE

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

docker run --rm --entrypoint /usr/bin/ffmpeg \
  "$image" -version > "$work/capture-ffmpeg.txt"
grep -q '^ffmpeg version ' "$work/capture-ffmpeg.txt"
