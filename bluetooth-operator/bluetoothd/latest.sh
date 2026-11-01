#!/usr/bin/env bash
#
# Report the buildroot commit that the bluetoothd image builds its
# static daemons with.
#
# bluetoothd/Dockerfile downloads buildroot at the commit in
# BUILDROOT_COMMIT and checks the tarball against BUILDROOT_SHA256.
# The two lines copy the pin of liken/userland, whose BR2_EXTERNAL tree
# the build reads, so the image and the OS build with one buildroot.
# liken/userland/latest.sh reports that pin and the BlueZ and dbus
# releases against their upstreams, and its --bump writes both copies.
# This row reads `behind` when the Dockerfile names another commit
# than liken/userland/VERSION, and `cd ci && go run . pins -root ..`
# fails in that case too.
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
