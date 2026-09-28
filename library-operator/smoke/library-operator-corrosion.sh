#!/usr/bin/env bash
# Proves the library-operator-corrosion image before it ships.
#
# Usage: library-operator-corrosion.sh <image>
#
#   <image>  the full reference of the Corrosion image, for example
#            ghcr.io/liken-sh/library-operator-corrosion:2026.10.02-001.
#            The image must be in the local Docker daemon.
#
# The runner has no peers, so nothing here replicates. What can break
# invisibly is the dynamic closure, so the binary proves that it loads
# its libraries by answering one flag.
set -euo pipefail

image=$1

docker run --rm "$image" --version
