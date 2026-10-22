#!/usr/bin/env bash
#
# Build the image that ./Dockerfile describes, and print its name.
# build.sh and catalog.sh both run in it. docker's layer cache makes a
# second build take a second.
#
# The build context is the CA bundle that the trust component pins,
# which trust/fetch.sh fetches and checks.

set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
trust="$(cd "$here/../../../../trust" && pwd)"
image="liken-ovmf-capsule-builder"

"$trust/fetch.sh" >/dev/null
docker build --quiet -t "$image" -f "$here/Dockerfile" \
    "$trust/dist/$(cat "$trust/VERSION")" >/dev/null
echo "$image"
