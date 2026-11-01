#!/usr/bin/env bash
#
# Report what the lab's firmware pins, and what Debian unstable has
# built now.
#
# The pin is the `ovmf-generic` package from Debian unstable, sid,
# which is the newest suite that Debian offers to install whole. The
# rule in root plan 83 takes that suite, not `experimental`, because
# `experimental` adds packages on top of sid. So both channel columns
# print `sid`.
#
# The report reads the newest version from the Packages index alone.
# A bump takes the digest through the archive's chain of signatures:
# it verifies sid's InRelease file against Debian's archive keys in
# debian-archive.asc, checks the Packages index against the digest
# that InRelease lists, and checks the package against the digest that
# the index lists. archive_deb in toolchains/pin.sh holds the steps.
# The key file holds the archive signing key of each Debian release
# that signs sid, because gpgv fails on a signature whose key it does
# not have. ftp-master.debian.org/keys.html lists those keys, and a new
# one is a change that a person makes by hand, after comparing it with
# the debian-archive-keyring package.
#
# Usage:
#   dev-cluster/firmware/latest.sh          report the pin
#   dev-cluster/firmware/latest.sh --bump   write the newest version and its digest

set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source-path=SCRIPTDIR source=../../../toolchains/pin.sh
source "$here/../../../toolchains/pin.sh"

pinned="$(cat "$here/VERSION")"

latest="$(fetch_text https://deb.debian.org/debian/dists/sid/main/binary-all/Packages.xz --max-time 120 |
    xz -dc |
    awk '/^Package: ovmf-generic$/ { found = 1 }
         found && /^Version:/ { print $2; found = 0 }')" || latest=""

row ovmf "$pinned" "$latest" sid sid - \
    "the lab's UEFI firmware, from Debian unstable; in no release"

[[ "${1:-}" == "--bump" ]] || exit 0

bump_needed ovmf "$pinned" "$latest" || exit 0

record="$(archive_deb "$here/debian-archive.asc" \
    https://deb.debian.org/debian/dists/sid main/binary-all/Packages.xz \
    ovmf-generic "$latest")"
digest="${record##* }"

echo "$latest" >"$here/VERSION"
set_var "$here/fetch.sh" digest "$digest"
bumped ovmf "$pinned" "$latest"
echo "the digest came from sid's Packages index, checked against InRelease, signed by Debian's archive keys"
next_step "make -C .. ovmf"
