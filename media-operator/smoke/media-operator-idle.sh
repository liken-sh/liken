#!/usr/bin/env bash
# Proves the media-operator-idle image before it ships.
#
# Usage: media-operator-idle.sh <image>
#
#   <image>  the full reference of the idle image, for example
#            ghcr.io/liken-sh/media-operator-idle:2026.10.02-001. The
#            image must be in the local Docker daemon.
#
# The CI runner has no GPU and no compositor, so the idle screen cannot
# draw here. `--help` prints the flags and exits before it opens a
# window. The dynamic loader refuses to start the binary when a library
# that it links is missing from the image, so a clean exit proves that
# every library is present. No other test catches a missing library.
set -euo pipefail

image=$1

# notices/check.sh fails unless the image contains these license files
# and the copyright file of each Debian package that its closures list.
"$(dirname "$0")/../../notices/check.sh" "$image" \
  usr/share/doc/rust/COPYRIGHT-library.html \
  usr/share/doc/fonts-droid-fallback/copyright \
  usr/share/doc/source-sans-3/LICENSE.md

docker run --rm "$image" --help > /dev/null
