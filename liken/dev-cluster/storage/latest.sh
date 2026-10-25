#!/usr/bin/env bash
#
# Report what this guest pins, and what Debian has built now.
#
# The pin is a dated build of Debian's generic cloud image, and the
# bytes come from cloud.debian.org, with the SHA512SUMS file of each
# build beside it. The channel is the Debian suite that the build
# comes from, the `suite` line in fetch.sh. Under the rule in root
# plan 83, a pin from a repository follows the newest suite that
# Debian offers to install whole, which is sid, not `experimental`,
# because `experimental` adds packages on top of sid. So the
# newest-channel column prints `sid`, and the latest column is the
# newest daily build of sid. A bare --bump moves the pin to that build
# and writes `sid` into fetch.sh.
#
# Each build directory lists the builds by date and serial, so the
# directory listing is the list of builds, and the newest name in it
# is the newest build. sid publishes daily builds only, and Debian
# deletes each one after about four months, so a sid pin expires
# unless it moves.
#
# This guest is the lab's storage server and ships in no release. It
# still gets watched, because a stale image is a drill against a
# server that stopped receiving security updates months ago.
#
# This pin carries no digest. Every build directory holds a SHA512SUMS
# file, and fetch.sh reads it on every fetch, so a bump writes one
# line in VERSION and, when the suite changes, one line in fetch.sh.
#
# Usage:
#   dev-cluster/storage/latest.sh          report the pin
#   dev-cluster/storage/latest.sh --bump   write the newest sid build

set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

newest_suite="sid"
images="https://cloud.debian.org/images/cloud/$newest_suite/daily"

pinned="$(cat "$here/VERSION")"
suite="$(sed -n 's|^suite="\([a-z]*\)"$|\1|p' "$here/fetch.sh")"

# The listing also carries names that are not builds, like latest, so
# the filter keeps only a date and a serial.
latest="$(curl -fsS --retry 3 --max-time 60 "$images/" |
    grep -oE 'href="[0-9]{8}-[0-9]+/"' |
    sed -e 's|href="||' -e 's|/"$||' |
    sort -V | tail -1)" || latest=""

printf '%s\t%s\t%s\t%s\t%s\t%s\t%s\n' storage "$pinned" "${latest:-?}" \
    "${suite:-?}" "$newest_suite" - "the lab's Debian cloud image; in no release"

[[ "${1:-}" == "--bump" ]] || exit 0

[[ -n "$latest" ]] || {
    echo "latest.sh: the image listing did not answer" >&2
    exit 1
}
[[ "$latest" != "$pinned" || "$suite" != "$newest_suite" ]] || {
    echo "the storage image $pinned is current"
    exit 0
}

echo "$latest" >"$here/VERSION"
sed -i "s|^suite=\"[a-z]*\"$|suite=\"$newest_suite\"|" "$here/fetch.sh"
echo "storage: $suite $pinned -> $newest_suite $latest"
echo "the build's SHA512SUMS file stands behind these bytes"
echo "next: make -C .. storage"
