#!/usr/bin/env bash
#
# Report what this domain pins, and what Canonical's mainline archive
# has now.
#
# The archive builds every upstream tag, and that includes release
# candidates: v7.2-rc5 sits in the same index as v7.1.5. liken boots
# releases, so this script drops every name with an -rc suffix and
# takes the highest of what is left.
#
# Not every tag in that index built successfully, and the index lists
# the directory either way. So a bump can name a version whose
# packages are missing, and kernel/fetch.sh is what says so: it
# reports "no mainline build found" and exits. That check belongs
# there, because it is the same check a hand-written pin needs.
#
# This pin carries no digest. Each mainline build publishes a
# CHECKSUMS file beside its packages, and fetch.sh reads it on every
# fetch, so a bump here is one line in VERSION.
#
# Usage:
#   kernel/latest.sh          report the pin and the newest release
#   kernel/latest.sh --bump   write the newest release into VERSION

set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

index="https://kernel.ubuntu.com/mainline/"

pinned="$(cat "$here/VERSION")"

# The index is an Apache directory listing, one link per built tag.
# Sorting needs the trailing slash gone: '.' sorts before '/', so
# "7.1.2/" would come out below "7.1/" and the newest release would
# be whichever one happened to be shortest.
latest="$(curl -fsS --retry 3 "$index" |
    grep -oE 'href="v[0-9][^"]*/"' |
    sed -e 's|href="v||' -e 's|/"$||' |
    grep -vE 'rc' |
    sort -V | tail -1)" || latest=""

printf '%s\t%s\t%s\t%s\n' kernel "$pinned" "${latest:-?}" \
    "Ubuntu mainline, newest build with no -rc"

[[ "${1:-}" == "--bump" ]] || exit 0

[[ -n "$latest" ]] || {
    echo "latest.sh: the mainline index did not answer" >&2
    exit 1
}
[[ "$latest" != "$pinned" ]] || {
    echo "kernel $pinned is current"
    exit 0
}

echo "$latest" >"$here/VERSION"
echo "kernel: $pinned -> $latest"
echo "the archive's CHECKSUMS file stands behind these bytes"
echo "next: make -C .. kernel"
