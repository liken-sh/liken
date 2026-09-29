#!/usr/bin/env bash
# Runs the mpv image once before it ships.
#
# Usage: mpv.sh <image>
#
#   <image>  the full reference of the mpv image, for example
#            ghcr.io/liken-sh/mpv:2026.10.02-001. The image must be in
#            the local Docker daemon.
#
# mpv opens the PipeWire client modules, the SPA plugins, and the
# fontconfig configuration by name, so a file the image lacks shows up
# at the first run and at no earlier point. The runner has no GPU, no
# compositor, and no sound server, so the run decodes five frames to no
# display and no audio; the driver load is proved on hardware. The
# version check writes to a file, because grep -q on a pipe would fail
# the check.
set -euo pipefail

image=$1

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

docker run --rm --entrypoint /usr/bin/mpv \
  "$image" --version > "$work/mpv.txt"
grep -q '^mpv v' "$work/mpv.txt"
docker run --rm --entrypoint /usr/bin/mpv \
  "$image" \
  --no-config --vo=null --ao=null --frames=5 \
  'av://lavfi:testsrc=size=64x64:rate=5'
