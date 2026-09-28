#!/usr/bin/env bash
#
# Report what this domain pins, and what snapshot curl has published
# now.
#
# The CA bundle has no version number. Each snapshot is named by the
# date Mozilla's store was extracted, and curl keeps every one of
# them. The extract page is the index of those dates, so the newest
# name on that page is the newest snapshot.
#
# Read what changed before you take a bump. A snapshot removes trust
# as well as adding it, and a certificate authority that leaves this
# file is one that every machine stops trusting on its next boot.
#
# This pin carries no digest. curl publishes a .sha256 file beside
# each snapshot, and trust/fetch.sh reads it on every fetch, so a bump
# here is one line in VERSION.
#
# Usage:
#   trust/latest.sh          report the pin and the newest snapshot
#   trust/latest.sh --bump   write the newest snapshot into VERSION

set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

extract="https://curl.se/docs/caextract.html"

pinned="$(cat "$here/VERSION")"

# The names are dates in ISO order, so a plain sort finds the newest.
latest="$(curl -fsS --retry 3 "$extract" |
    grep -oE 'cacert-[0-9]{4}-[0-9]{2}-[0-9]{2}\.pem' |
    sed -e 's|^cacert-||' -e 's|\.pem$||' |
    sort | tail -1)" || latest=""

printf '%s\t%s\t%s\t%s\n' trust "$pinned" "${latest:-?}" \
    "newest Mozilla extract that curl publishes"

[[ "${1:-}" == "--bump" ]] || exit 0

[[ -n "$latest" ]] || {
    echo "latest.sh: the extract page did not answer" >&2
    exit 1
}
[[ "$latest" != "$pinned" ]] || {
    echo "the CA bundle $pinned is current"
    exit 0
}

echo "$latest" >"$here/VERSION"
echo "trust: $pinned -> $latest"
echo "curl's .sha256 file stands behind these bytes"
echo "next: make -C .. trust"
