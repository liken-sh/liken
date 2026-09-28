#!/usr/bin/env bash
# Proves the library-operator-media-browser image before it ships.
#
# Usage: library-operator-media-browser.sh <image>
#
#   <image>  the full reference of the media browser image, for
#            example
#            ghcr.io/liken-sh/library-operator-media-browser:2026.10.02-001.
#            The image must be in the local Docker daemon.
#
# The runner has no GPU and no Wayland, so nothing here draws. What can
# break invisibly is the dynamic closure, so the binary proves that it
# loads its libraries by answering one flag.
set -euo pipefail

image=$1

docker run --rm "$image" --help
