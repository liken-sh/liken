#!/usr/bin/env bash
# Proves the media-operator-idle image before it ships.
#
# Usage: media-operator-idle.sh <image>
#
#   <image>  the full reference of the idle image, for example
#            ghcr.io/liken-sh/media-operator-idle:2026.10.02-001. The
#            image must be in the local Docker daemon.
#
# The check on the idle image takes the same shape as the player's
# check. The runner has no GPU and no compositor, so the client cannot
# draw; what can break invisibly is the closure. The binary prints its
# flags without opening a window, which proves it loaded every library
# it links.
set -euo pipefail

image=$1

docker run --rm "$image" --help > /dev/null
