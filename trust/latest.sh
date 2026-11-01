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
# `liken` takes each snapshot as curl publishes it, and no package
# repository stands between, so the pin has no channel and both
# channel columns print `-`. Under the rule in root plan 83 it
# follows the newest snapshot.
#
# Read what changed before you take a bump. A snapshot removes trust
# as well as adding it, and a certificate authority that leaves this
# file is one that every machine stops trusting on its next boot.
#
# --bump reads curl's .sha256 file beside the new snapshot and writes
# the digest into trust/pin.toml, which trust/fetch.sh, the OS build's
# fetch, reads, and into the trust image's Dockerfile, which fetches by
# it. `ci pins` fails when the two differ.
#
# Usage:
#   trust/latest.sh          report the pin and the newest snapshot
#   trust/latest.sh --bump   write the newest snapshot into VERSION,
#                            pin.toml, package.toml, and the
#                            Dockerfile

set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source-path=SCRIPTDIR source=../toolchains/pin.sh
source "$here/../toolchains/pin.sh"

extract="https://curl.se/docs/caextract.html"

pinned="$(cat "$here/VERSION")"

# The names are dates in ISO order, so a plain sort finds the newest.
latest="$(fetch_text "$extract" |
    grep -oE 'cacert-[0-9]{4}-[0-9]{2}-[0-9]{2}\.pem' |
    sed -e 's|^cacert-||' -e 's|\.pem$||' |
    sort | tail -1)" || latest=""

row trust "$pinned" "$latest" - - - "newest Mozilla extract that curl publishes"

[[ "${1:-}" == "--bump" ]] || exit 0

bump_needed "the CA bundle" "$pinned" "$latest" || exit 0

# The pin is the date, in the places that state it: VERSION, which the
# OS build reads; pin.toml, which the OS build's fetch and the SBOM
# read; the version of the trust image in package.toml; and the
# snapshot that the Dockerfile fetches by its checksum. A new date
# starts the image's revisions at 1 again.
#
# curl.se publishes no signature of the bundle or of its .sha256 file,
# so the digest rests on the HTTPS connection to curl.se alone.
digest="$(fetch_text "https://curl.se/ca/cacert-$latest.pem.sha256" | cut -d' ' -f1)" || digest=""
[[ "$digest" =~ ^[0-9a-f]{64}$ ]] || fail "curl.se published no sha256 for $latest"
url="https://curl.se/ca/cacert-$latest.pem"
echo "$latest" >"$here/VERSION"
set_pin cacert "$latest" "$url" "$digest" "$(generic_purl cacert "$latest" "$url" "$digest")"
set_line "$here/package.toml" '^version = "[0-9]*"$' "version = \"${latest//-/}\""
set_line "$here/package.toml" '^revision = [0-9]*$' "revision = 1"
set_line "$here/package.toml" '^mozilla-ca = ".*"$' "mozilla-ca = \"$latest\""
sed -i -e "s/--checksum=sha256:[0-9a-f]*/--checksum=sha256:$digest/" \
    -e "s|cacert-[0-9-]*\.pem|cacert-$latest.pem|" "$here/Dockerfile"
bumped trust "$pinned" "$latest"
echo "curl's .sha256 file stands behind these bytes; curl.se signs neither file"
next_step "make workflows at the top of the repository, raise the revision"
echo "of each pinned component that copies the bundle, and make -C ../liken trust"
