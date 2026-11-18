#!/usr/bin/env bash
# Proves the media-operator-api image before it ships.
#
# Usage: media-operator-api.sh <image>
#
#   <image>  the full reference of the api image, for example
#            ghcr.io/liken-sh/media-operator-api:2026.10.02-001. The
#            image must be in the local Docker daemon.
#
# The API muxes each capture stream with ffmpeg, so the check runs the
# image's ffmpeg in place of the entrypoint to prove that it starts.
set -euo pipefail

image=$1

# notices/check.sh fails unless the image contains these license files
# and the copyright file of each Debian package that its closures list.
"$(dirname "$0")/../../notices/check.sh" "$image" \
  usr/share/doc/go/LICENSE

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

docker run --rm --entrypoint /usr/bin/ffmpeg "$image" -version > "$work/ffmpeg.txt"
grep -q '^ffmpeg version ' "$work/ffmpeg.txt"
