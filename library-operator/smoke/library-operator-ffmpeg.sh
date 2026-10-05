#!/usr/bin/env bash
# Proves the library-operator-ffmpeg image before it ships.
#
# Usage: library-operator-ffmpeg.sh <image>
#
#   <image>  the full reference of the ffmpeg image, for example
#            ghcr.io/liken-sh/library-operator-ffmpeg:2026.10.02-001.
#            The image must be in the local Docker daemon.
#
# The runner has no GPU, so nothing here decodes on hardware. What can
# break invisibly is the dynamic closure, so each binary proves that it
# loads its libraries by answering one flag.
set -euo pipefail

image=$1

# The image must hold the notices of the third-party software in it,
# as notices/check.sh describes.
"$(dirname "$0")/../../notices/check.sh" "$image" \
  usr/share/doc/go/LICENSE \
  usr/share/doc/mozilla-ca/copyright

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

docker run --rm --entrypoint /usr/bin/ffprobe "$image" -version
docker run --rm --entrypoint /usr/bin/ffmpeg "$image" -version
# The runner has no GPU to decode on, so the check is that this ffmpeg
# was built with the VA-API decoder at all. grep -q closes the pipe
# early, docker dies on the closed pipe, and pipefail fails the check,
# so the output goes to a file first.
docker run --rm --entrypoint /usr/bin/ffmpeg "$image" -hwaccels > "$work/hwaccels.txt"
grep -q vaapi "$work/hwaccels.txt"
