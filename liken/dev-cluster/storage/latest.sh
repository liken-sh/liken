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
# The pin carries the image's digest in fetch.sh. Every build
# directory holds a SHA512SUMS file, and --bump copies the image's
# line from it into fetch.sh, with the suite line when the suite
# changes. Debian publishes no signature of the SHA512SUMS file of a
# cloud image build, so the digest rests on the HTTPS connection to
# cloud.debian.org alone.
#
# Usage:
#   dev-cluster/storage/latest.sh          report the pin
#   dev-cluster/storage/latest.sh --bump   write the newest sid build

set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source-path=SCRIPTDIR source=../../../toolchains/pin.sh
source "$here/../../../toolchains/pin.sh"

newest_suite="sid"
images="https://cloud.debian.org/images/cloud/$newest_suite/daily"

pinned="$(cat "$here/VERSION")"
suite="$(sed -n 's|^suite="\([a-z]*\)"$|\1|p' "$here/fetch.sh")"

# The listing also carries names that are not builds, like latest, so
# the filter keeps only a date and a serial.
latest="$(fetch_text "$images/" |
    grep -oE 'href="[0-9]{8}-[0-9]+/"' |
    sed -e 's|href="||' -e 's|/"$||' |
    sort -V | tail -1)" || latest=""

row storage "$pinned" "$latest" \
    "${suite:-?}" "$newest_suite" - "the lab's Debian cloud image; in no release"

[[ "${1:-}" == "--bump" ]] || exit 0

# A pin on another suite moves to sid even when the build names match.
[[ "$suite" != "$newest_suite" ]] || bump_needed "the storage image" "$pinned" "$latest" || exit 0
[[ -n "$latest" ]] || fail "the image listing did not answer"

# sid's daily builds name the image with the word daily and the build.
image="debian-sid-genericcloud-amd64-daily-$latest.qcow2"
digest="$(fetch_text "$images/$latest/SHA512SUMS" |
    awk -v img="$image" '$2 == img { print $1 }')" || digest=""
[[ "$digest" =~ ^[0-9a-f]{128}$ ]] || fail "$images/$latest/SHA512SUMS lists no $image"

echo "$latest" >"$here/VERSION"
set_line "$here/fetch.sh" '^suite="[a-z]*"$' "suite=\"$newest_suite\""
set_entry "$here/fetch.sh" image_sha512 "$latest" "$digest"
bumped storage "$suite $pinned" "$newest_suite $latest"
echo "the digest came from the build's SHA512SUMS file, which Debian does not sign"
next_step "make -C .. storage"
