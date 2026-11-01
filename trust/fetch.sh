#!/usr/bin/env bash
#
# Vendor the machine's trust store: the CA certificates it uses to
# verify who it is talking to when it pulls container images over TLS.
#
# This domain is the counterpart of the identity domain. The identity
# domain mints the machine's own certificate authorities. This domain
# vendors the roots the machine trusts: the Mozilla CA program's root
# certificates, the same set nearly every Linux distribution ships at
# /etc/ssl/certs. There is no way to avoid taking someone's word for
# which roots belong there. Mozilla's list is the most heavily
# scrutinized one available. The curl project publishes it, extracted
# from Firefox's source and converted to PEM, as dated immutable
# snapshots.
#
# So the pin here is a date (trust/VERSION) and a sha256 digest, and
# this script verifies the fetch against that digest, the same as
# every other vendored input. As a result, the image's trust store
# does not depend on whichever build host assembled it. A change in
# what the machine trusts shows up in this repo as a deliberate,
# reviewable version bump.
#
# Usage:
#   trust/fetch.sh               fetch the snapshot pinned in trust/VERSION
#   trust/fetch.sh 2026-03-11    fetch another snapshot, which the
#                                Dockerfile must name with its digest
#
# Results land in trust/dist/<version>/cacert.pem, cached in trust/cache/.

set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source-path=SCRIPTDIR source=../toolchains/pin.sh
source "$here/../toolchains/pin.sh"

require_tools sha256sum

version="${1:-$(cat "$here/VERSION")}"
url="https://curl.se/ca/cacert-$version.pem"

cache="$here/cache/$version"
out="$here/dist/$version"

# The digest comes from the trust image's Dockerfile. Its ADD line
# names the snapshot's URL with a --checksum on the line before, and
# trust/latest.sh --bump writes both from curl's .sha256 file. One pin
# serves the image and this fetch, so the two cannot disagree, and a
# fetch never reads a digest from curl.se.
digest="$(awk -v url="$url" '
    index($0, url) && prev ~ /--checksum=sha256:/ {
        sub(/.*--checksum=sha256:/, "", prev)
        sub(/[^0-9a-f].*/, "", prev)
        print prev
    }
    { prev = $0 }' "$here/Dockerfile")"
[[ "$digest" =~ ^[0-9a-f]{64}$ ]] ||
    fail "trust/Dockerfile pins no digest for $url; trust/latest.sh --bump writes it"

fetch_verified "$url" "$digest" "$cache/cacert.pem"

rm -rf "$out"
mkdir -p "$out"
cp "$cache/cacert.pem" "$out/cacert.pem"

echo
echo "CA bundle $version:"
grep -c "BEGIN CERTIFICATE" "$out/cacert.pem" | xargs -I{} echo "{} certificates"
