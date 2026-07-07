#!/usr/bin/env bash
#
# Vendor the machine's trust store: the CA certificates that let it
# verify who it's talking to when it pulls container images over TLS.
#
# This domain is the counterpart of the identity domain. The identity
# domain mints the machine's own certificate authorities; this domain
# vendors the roots the machine trusts: the Mozilla CA program's root
# certificates, the same set nearly every Linux distribution ships at
# /etc/ssl/certs. There is no way around taking someone's word for
# which roots belong there. Mozilla's list is the most heavily
# scrutinized one available, and the curl project publishes it,
# extracted from Firefox's source and converted to PEM, as dated
# immutable snapshots.
#
# So the pin here is a date (trust/VERSION), and the fetch is verified
# against the sha256 published beside the snapshot, just like every
# other vendored input. As a result, the image's trust store doesn't
# depend on whichever build host assembled it, and a change in what
# the machine trusts shows up in this repo as a deliberate, reviewable
# version bump.
#
# Usage:
#   trust/fetch.sh               fetch the snapshot pinned in trust/VERSION
#   trust/fetch.sh 2026-03-11    fetch a specific snapshot instead
#
# Results land in trust/dist/<version>/cacert.pem, cached in trust/cache/.

set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

for tool in curl sha256sum; do
    command -v "$tool" >/dev/null || {
        echo "fetch.sh: missing required tool: $tool" >&2
        exit 1
    }
done

version="${1:-$(cat "$here/VERSION")}"
url="https://curl.se/ca/cacert-$version.pem"

cache="$here/cache/$version"
out="$here/dist/$version"
mkdir -p "$cache"

digest="$(curl -fsSL "$url.sha256" | awk '{ print $1 }')"
if [[ -z "$digest" ]]; then
    echo "fetch.sh: no checksum published at $url.sha256" >&2
    exit 1
fi

if ! sha256sum --check --status <<<"$digest  $cache/cacert.pem" >/dev/null 2>&1; then
    echo "downloading CA bundle $version"
    curl -fL --progress-bar -o "$cache/cacert.pem" "$url"
    sha256sum --check --quiet <<<"$digest  $cache/cacert.pem"
fi

rm -rf "$out"
mkdir -p "$out"
cp "$cache/cacert.pem" "$out/cacert.pem"

echo
echo "CA bundle $version:"
grep -c "BEGIN CERTIFICATE" "$out/cacert.pem" | xargs -I{} echo "{} certificates"
