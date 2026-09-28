#!/usr/bin/env bash
# Runs the ffmpeg image once before it ships.
#
# Usage: ffmpeg.sh <image>
#
#   <image>  the full reference of the ffmpeg image, for example
#            ghcr.io/liken-sh/ffmpeg:2026.10.02-001. The image must be
#            in the local Docker daemon.
#
# ffmpeg opens libva, and libva opens the driver, by file name, so a
# library the image lacks shows up at the first run and at no earlier
# point. The runner has no GPU, so the run proves the software path and
# that vaapi is compiled in; the driver load is proved on hardware.
set -euo pipefail

image=$1

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

docker run --rm "$image" \
  -f lavfi -i testsrc=duration=1:size=64x64:rate=5 -f null -
# grep -q closes the pipe early, docker dies on the closed pipe, and
# pipefail fails the check, so each check writes the output to a file
# first.
docker run --rm --entrypoint /usr/bin/ffprobe \
  "$image" -version > "$work/ffprobe.txt"
grep -q '^ffprobe version ' "$work/ffprobe.txt"
docker run --rm "$image" \
  -hide_banner -hwaccels > "$work/hwaccels.txt"
grep -q vaapi "$work/hwaccels.txt"
