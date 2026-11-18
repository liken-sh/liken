#!/usr/bin/env bash
# Checks the media-operator-player image before it is published.
#
# Usage: media-operator-player.sh <image>
#
#   <image>  the full reference of the player image, for example
#            ghcr.io/liken-sh/media-operator-player:2026.10.02-001. The
#            image must be in the local Docker daemon.
#
# The CI runner has no GPU, Wayland, or PipeWire, so the check cannot
# play anything. It checks the files of the image instead: mpv must
# load its libraries and print its version, and /media-operator-pod
# must exist.
set -euo pipefail

image=$1

# Checks the license notices of the third-party software in the image
# (notices/check.sh).
"$(dirname "$0")/../../notices/check.sh" "$image" \
  usr/share/doc/go/LICENSE \
  usr/share/doc/fonts-droid-fallback/copyright

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

docker run --rm --entrypoint /usr/bin/mpv "$image" --version
# The listing goes to a file because `grep -q` exits at the first match,
# which kills tar on the closed pipe and fails the pipeline under
# pipefail.
container=$(docker create "$image")
docker export "$container" | tar -t > "$work/image-files.txt"
docker rm "$container" > /dev/null
grep -qx 'media-operator-pod' "$work/image-files.txt" \
  || { echo "the player image has no /media-operator-pod"; exit 1; }
