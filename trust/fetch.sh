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
# So the pin here is a date (trust/VERSION) and a sha256 digest
# (trust/pin.toml), and this script verifies the fetch against that
# digest, the same as every other vendored input. As a result, the image's trust store
# does not depend on whichever build host assembled it. A change in
# what the machine trusts shows up in this repo as a deliberate,
# reviewable version bump.
#
# Usage:
#   trust/fetch.sh               fetch the snapshot pinned in trust/VERSION
#
# Results land in trust/dist/<version>/cacert.pem, cached in trust/cache/.

set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source-path=SCRIPTDIR source=../toolchains/pin.sh
source "$here/../toolchains/pin.sh"

require_tools sha256sum

version="$(cat "$here/VERSION")"

cache="$here/cache/$version"
out="$here/dist/$version"

# trust/latest.sh --bump writes the snapshot's URL and digest from
# curl's .sha256 file into trust/pin.toml and into the ADD line of the
# trust image's Dockerfile, and `ci pins` fails when the two differ, so
# the image and this fetch take the same bytes.
fetch_pinned cacert "$version" "$cache/cacert.pem"

rm -rf "$out"
mkdir -p "$out"
cp "$cache/cacert.pem" "$out/cacert.pem"

echo
echo "CA bundle $version:"
grep -c "BEGIN CERTIFICATE" "$out/cacert.pem" | xargs -I{} echo "{} certificates"
