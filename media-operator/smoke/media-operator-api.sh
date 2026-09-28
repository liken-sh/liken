#!/usr/bin/env bash
# Proves the media-operator-api image before it ships.
#
# Usage: media-operator-api.sh <image>
#
#   <image>  the full reference of the api image, for example
#            ghcr.io/liken-sh/media-operator-api:2026.10.02-001. The
#            image must be in the local Docker daemon.
#
# The check proves ffmpeg is in the image and runs. The composed stream
# is one ffmpeg mux, and the image has no shell, so the check runs
# ffmpeg itself as the entrypoint.
set -euo pipefail

image=$1

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

docker run --rm --entrypoint /usr/bin/ffmpeg "$image" -version > "$work/ffmpeg.txt"
grep -q '^ffmpeg version ' "$work/ffmpeg.txt"
