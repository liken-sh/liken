#!/usr/bin/env bash
# Proves the media-operator-display image before it ships.
#
# Usage: media-operator-display.sh <image>
#
#   <image>  the full reference of the display image, for example
#            ghcr.io/liken-sh/media-operator-display:2026.10.02-001.
#            The image must be in the local Docker daemon.
#
# The binary takes no flag that prints and exits, so the loader in
# `--list` mode resolves the whole closure and exits non-zero when a
# library is missing. A library the toolkit opens by name at run time
# is not covered by this.
set -euo pipefail

image=$1

docker run --rm --entrypoint /lib64/ld-linux-x86-64.so.2 \
  "$image" \
  --list /media-display > /dev/null
