#!/usr/bin/env bash
#
# Report what this guest pins, and what Debian has built now.
#
# The pin is a dated cloud image build, and Debian publishes each one
# into its own immutable directory. So the directory listing is the
# list of builds, and the newest name in it is the newest build.
#
# This guest is the lab's storage server and ships in no release. It
# still gets watched, because a stale image is a drill against a
# server that stopped receiving security updates months ago.
#
# This pin carries no digest. Every build directory holds a SHA512SUMS
# file, and fetch.sh reads it on every fetch, so a bump here is one
# line in VERSION.
#
# Usage:
#   dev-cluster/storage/latest.sh          report the pin
#   dev-cluster/storage/latest.sh --bump   write the newest build

set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

images="https://cloud.debian.org/images/cloud/trixie"

pinned="$(cat "$here/VERSION")"

# The listing also carries names that are not builds, like daily and
# latest, so the filter keeps only a date and a serial.
latest="$(curl -fsS --retry 3 "$images/" |
    grep -oE 'href="[0-9]{8}-[0-9]+/"' |
    sed -e 's|href="||' -e 's|/"$||' |
    sort -V | tail -1)" || latest=""

printf '%s\t%s\t%s\t%s\n' storage "$pinned" "${latest:-?}" \
    "the lab's Debian cloud image; in no release"

[[ "${1:-}" == "--bump" ]] || exit 0

[[ -n "$latest" ]] || {
    echo "latest.sh: the image listing did not answer" >&2
    exit 1
}
[[ "$latest" != "$pinned" ]] || {
    echo "the storage image $pinned is current"
    exit 0
}

echo "$latest" >"$here/VERSION"
echo "storage: $pinned -> $latest"
echo "the build's SHA512SUMS file stands behind these bytes"
echo "next: make -C .. storage"
