#!/usr/bin/env bash
#
# Report what this domain pins, and what kernel.org has published now.
#
# The firmware project cuts a release every few weeks and names it for
# the date. There is no tag list to read here, because the fetch takes
# the release tarball from kernel.org, and that directory listing is
# the index of every release.
#
# kernel.org publishes one sha256sums file for the whole directory, so
# a bump reads the new digest from upstream rather than from its own
# download. That matters more here than anywhere else in the
# repository: this tarball is the largest thing liken fetches, and a
# bump that had to hash it would download it twice.
#
# Usage:
#   linux-firmware/latest.sh          report the pin and the newest release
#   linux-firmware/latest.sh --bump   write the newest release and its digest

set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

archive="https://cdn.kernel.org/pub/linux/kernel/firmware"

pinned="$(cat "$here/VERSION")"

latest="$(curl -fsS --retry 3 "$archive/" |
    grep -oE 'linux-firmware-[0-9]{8}\.tar\.xz' |
    sed -e 's|^linux-firmware-||' -e 's|\.tar\.xz$||' |
    sort | tail -1)" || latest=""

printf '%s\t%s\t%s\t%s\n' linux-firmware "$pinned" "${latest:-?}" \
    "newest release tarball on kernel.org"

[[ "${1:-}" == "--bump" ]] || exit 0

[[ -n "$latest" ]] || {
    echo "latest.sh: the firmware directory did not answer" >&2
    exit 1
}
[[ "$latest" != "$pinned" ]] || {
    echo "linux-firmware $pinned is current"
    exit 0
}

# The sums file is a clearsigned document, so the digest lines sit
# among the signature's own text. Matching the exact filename picks
# the one line that matters.
tarball="linux-firmware-$latest.tar.xz"
digest="$(curl -fsSL --retry 3 "$archive/sha256sums.asc" |
    awk -v t="$tarball" '$2 == t { print $1 }')"
[[ -n "$digest" ]] || {
    echo "latest.sh: no $tarball listed in $archive/sha256sums.asc" >&2
    exit 1
}

echo "$latest" >"$here/VERSION"
sed -i "s|^digest=\".*\"|digest=\"$digest\"|" "$here/fetch.sh"
echo "linux-firmware: $pinned -> $latest"
echo "kernel.org's sha256sums file stands behind these bytes"
echo "the microcode domain builds AMD's blobs from this tarball; rebuild it too"
echo "next: make -C .. linux-firmware microcode"
