#!/usr/bin/env bash
#
# Build the image that ./Dockerfile describes, and print its name.
# build.sh and catalog.sh both run in it. docker's layer cache makes a
# second build take a second.
#
# The build context is the CA bundle that the trust component pins,
# which trust/fetch.sh fetches and checks. The Dockerfile builds FROM
# ubuntu, and the named context ubuntu is the reference with its digest
# that the `ubuntu` entry of toolchains/images.toml gives, the way the
# bake file resolves the same name for the images of the repository.

set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
root="$(cd "$here/../../../.." && pwd)"
trust="$root/trust"
image="liken-ovmf-capsule-builder"

ubuntu="$(sed -n 's/^ubuntu = "\(.*@sha256:[0-9a-f]*\)"$/\1/p' "$root/toolchains/images.toml")"
[[ -n "$ubuntu" ]] || {
    echo "builder.sh: toolchains/images.toml names no ubuntu image by digest" >&2
    exit 1
}

"$trust/fetch.sh" >/dev/null
docker build --quiet -t "$image" -f "$here/Dockerfile" \
    --build-context "ubuntu=docker-image://$ubuntu" \
    "$trust/dist/$(cat "$trust/VERSION")" >/dev/null
echo "$image"
