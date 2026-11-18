#!/usr/bin/env bash
# Checks the media-operator-display image before it is published.
#
# Usage: media-operator-display.sh <image>
#
#   <image>  the full reference of the display image, for example
#            ghcr.io/liken-sh/media-operator-display:2026.10.02-001.
#            The image must be in the local Docker daemon.
#
# The binary has no flag that prints and exits. Instead the dynamic
# loader runs with `--list`, which resolves every linked library and
# exits non-zero when one is missing. This does not detect a library
# that the program opens by name at run time.
set -euo pipefail

image=$1

# Checks the license notices of the third-party software in the image
# (notices/check.sh).
"$(dirname "$0")/../../notices/check.sh" "$image" \
  usr/share/doc/rust/COPYRIGHT-library.html \
  usr/share/doc/fonts-droid-fallback/copyright

docker run --rm --entrypoint /lib64/ld-linux-x86-64.so.2 \
  "$image" \
  --list /media-display > /dev/null
