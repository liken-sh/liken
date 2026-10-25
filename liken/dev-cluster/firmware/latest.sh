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
# Debian's signed Packages index for unstable lists the current
# ovmf-generic version with its SHA256, so a bump takes both from the
# same record and needs no download.
#
# Usage:
#   dev-cluster/firmware/latest.sh          report the pin
#   dev-cluster/firmware/latest.sh --bump   write the newest version and its digest

set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

pinned="$(cat "$here/VERSION")"

record="$(curl -fsS --retry 3 --max-time 120 https://deb.debian.org/debian/dists/sid/main/binary-all/Packages.xz |
    xz -dc |
    awk '/^Package: ovmf-generic$/ { found = 1 }
         found && /^Version:/ { version = $2 }
         found && /^SHA256:/ { print version, $2; found = 0 }')" || record=""
latest="${record%% *}"
digest="${record##* }"

printf '%s\t%s\t%s\t%s\t%s\t%s\t%s\n' ovmf "$pinned" "${latest:-?}" sid sid - \
    "the lab's UEFI firmware, from Debian unstable; in no release"

[[ "${1:-}" == "--bump" ]] || exit 0

[[ -n "$latest" ]] || {
    echo "latest.sh: the Packages index did not answer" >&2
    exit 1
}
[[ "$latest" != "$pinned" ]] || {
    echo "ovmf $pinned is current"
    exit 0
}

echo "$latest" >"$here/VERSION"
sed -i "s|^digest=\".*\"|digest=\"$digest\"|" "$here/fetch.sh"
echo "ovmf: $pinned -> $latest"
echo "the digest came from Debian's signed Packages index"
echo "next: make -C .. ovmf"
