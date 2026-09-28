#!/usr/bin/env bash
#
# Report what this domain pins, and what the hwdata project has
# tagged now.
#
# The pin is a git tag, because that is what the fetch names: the
# project publishes no release archive for this file, so fetch.sh
# reads pci.ids straight out of the tagged tree. The tag list comes
# from git for the same reason it does in the other domains here: no
# token, no rate limit.
#
# Nothing upstream signs or checksums this file, so a bump computes
# the digest from its own download. That pins the bytes for every
# later rebuild. It proves nothing about where they came from, which
# is why the bump says so.
#
# Usage:
#   hwdata/latest.sh          report the pin and the newest tag
#   hwdata/latest.sh --bump   write the newest tag and its digest

set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

repository="https://github.com/vcrhonek/hwdata"

pinned="$(cat "$here/VERSION")"

latest="$(git ls-remote --tags --refs "$repository" |
    sed 's|.*refs/tags/||' |
    grep -E '^v[0-9]+\.[0-9]+$' |
    sort -V | tail -1)" || latest=""

printf '%s\t%s\t%s\t%s\n' hwdata "$pinned" "${latest:-?}" \
    "newest tag; the file ships from the tagged tree"

[[ "${1:-}" == "--bump" ]] || exit 0

[[ -n "$latest" ]] || {
    echo "latest.sh: the tag list did not answer" >&2
    exit 1
}
[[ "$latest" != "$pinned" ]] || {
    echo "hwdata $pinned is current"
    exit 0
}

url="https://raw.githubusercontent.com/vcrhonek/hwdata/$latest/pci.ids"
digest="$(curl -fsSL --retry 3 "$url" | sha256sum | cut -d' ' -f1)"

echo "$latest" >"$here/VERSION"
sed -i "s|^digest=\".*\"|digest=\"$digest\"|" "$here/fetch.sh"
echo "hwdata: $pinned -> $latest"
echo "the digest came from this download; nothing upstream signs it"
echo "next: make -C .. hwdata"
