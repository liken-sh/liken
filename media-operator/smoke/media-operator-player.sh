#!/usr/bin/env bash
# Proves the media-operator-player image before it ships.
#
# Usage: media-operator-player.sh <image>
#
#   <image>  the full reference of the player image, for example
#            ghcr.io/liken-sh/media-operator-player:2026.10.02-001. The
#            image must be in the local Docker daemon.
#
# The runner has no GPU, no Wayland, and no PipeWire, so nothing here
# plays; what can break invisibly is the closure. mpv proves it can
# load its libraries by printing its version, and the supervisor binary
# must be present at the path the entrypoint names.
set -euo pipefail

image=$1

# The image must hold the notices of the third-party software in it,
# as notices/check.sh describes.
"$(dirname "$0")/../../notices/check.sh" "$image" \
  usr/share/doc/go/LICENSE \
  usr/share/doc/fonts-droid-fallback/copyright

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

docker run --rm --entrypoint /usr/bin/mpv "$image" --version
# grep -q closes the pipe early, tar dies on the closed pipe, and
# pipefail fails the check, so the listing goes to a file first.
container=$(docker create "$image")
docker export "$container" | tar -t > "$work/image-files.txt"
docker rm "$container" > /dev/null
grep -qx 'media-operator-pod' "$work/image-files.txt" \
  || { echo "the player image has no /media-operator-pod"; exit 1; }
