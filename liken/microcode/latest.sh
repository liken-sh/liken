#!/usr/bin/env bash
#
# Report what this domain pins, and what Intel has released now.
#
# Only the Intel half of this domain has a pin of its own. The AMD
# blobs come out of the linux-firmware tarball, and fetch.sh reads
# linux-firmware/VERSION directly, so that half cannot drift from the
# domain that owns it.
#
# Intel names each release for the date it was cut, and reissues one
# as a -rev tag when a build has to be replaced. The version sort
# keeps those in order behind their date, so the newest tag wins
# either way.
#
# Nothing upstream checksums the GitHub source archive, so a bump
# computes the digest from its own download.
#
# Usage:
#   microcode/latest.sh          report the pin and the newest release
#   microcode/latest.sh --bump   write the newest release and its digest

set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

repository="https://github.com/intel/Intel-Linux-Processor-Microcode-Data-Files"

pinned="$(cat "$here/VERSION")"

latest="$(git ls-remote --tags --refs "$repository" |
    sed 's|.*refs/tags/microcode-||' |
    grep -E '^[0-9]{8}' |
    sort -V | tail -1)" || latest=""

printf '%s\t%s\t%s\t%s\n' microcode "$pinned" "${latest:-?}" \
    "Intel's data files; the AMD blobs follow linux-firmware"

[[ "${1:-}" == "--bump" ]] || exit 0

[[ -n "$latest" ]] || {
    echo "latest.sh: the tag list did not answer" >&2
    exit 1
}
[[ "$latest" != "$pinned" ]] || {
    echo "the Intel microcode $pinned is current"
    exit 0
}

url="$repository/archive/refs/tags/microcode-$latest.tar.gz"
digest="$(curl -fsSL --retry 3 "$url" | sha256sum | cut -d' ' -f1)"

echo "$latest" >"$here/VERSION"
sed -i "s|^digest=\".*\"|digest=\"$digest\"|" "$here/fetch.sh"
echo "microcode: $pinned -> $latest"
echo "the digest came from this download; nothing upstream signs it"
echo "next: make -C .. microcode"
