#!/usr/bin/env bash
#
# Report the buildroot commit that the bluetoothd image builds its
# static daemons with.
#
# `BUILDROOT_COMMIT` and `BUILDROOT_SHA256` in `bluetoothd/Dockerfile`
# copy the buildroot pin of `liken/userland`, because the image build
# reads the BR2_EXTERNAL tree of `liken/userland` and must use the same
# buildroot as the OS. `liken/userland/latest.sh --bump` writes both
# copies. This row reads `behind` when the Dockerfile's commit differs
# from `liken/userland/VERSION`, and `cd ci && go run . pins -root ..`
# fails then too.
#
# Usage:
#   bluetoothd/latest.sh   report the buildroot commit of the image

set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source-path=SCRIPTDIR source=../../toolchains/pin.sh
source "$here/../../toolchains/pin.sh"

pinned="$(sed -n 's/^ARG BUILDROOT_COMMIT=//p' "$here/Dockerfile")"
userland="$(cat "$here/../../liken/userland/VERSION")"

row buildroot "${pinned:0:12}" "${userland:0:12}" - - - \
    "follows liken/userland/VERSION; liken/userland/latest.sh --bump writes it"

[[ "${1:-}" != "--bump" ]] || fail "liken/userland/latest.sh --bump moves this pin"
