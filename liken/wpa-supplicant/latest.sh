#!/usr/bin/env bash
#
# Report what this domain pins, and what hostap has released now.
#
# The pin is the supplicant's source. A hostap release is a numbered
# tarball on w1.fi, cut every year or two, with a matching hostap_2_N
# tag in the project's git. w1.fi publishes no checksum file, so a
# bump computes the digest from its own download. It does publish a
# detached PGP signature, which this domain does not check; the bump
# says so and names the file to check by hand.
#
# `liken` builds this domain from source, so the pin has no channel,
# and both channel columns print `-`. Under the rule in root plan 83,
# the pin follows the newest release of its upstream project. The
# image that the build runs in is the `alpine` entry in
# toolchains/images.toml, and toolchains/latest.sh reports it.
#
# Usage:
#   wpa-supplicant/latest.sh          report the pin
#   wpa-supplicant/latest.sh --bump   move the supplicant

set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source-path=SCRIPTDIR source=../../toolchains/pin.sh
source "$here/../../toolchains/pin.sh"

releases="https://w1.fi/releases"

pinned="$(cat "$here/VERSION")"
# hostap publishes one tarball per release in this one directory, so
# the names in the directory listing are the release list.
latest="$(fetch_text "$releases/" |
    grep -oE 'wpa_supplicant-[0-9]+\.[0-9]+\.tar\.gz' |
    sed -e 's|wpa_supplicant-||' -e 's|\.tar\.gz||' |
    sort -V | tail -1)" || latest=""
row wpa-supplicant "$pinned" "$latest" - - - "newest release on w1.fi"

[[ "${1:-}" == "--bump" ]] || exit 0

case "${2:-wpa-supplicant}" in
wpa-supplicant)
    bump_needed wpa_supplicant "$pinned" "$latest" || exit 0
    digest="$(digest_of_url "$releases/wpa_supplicant-$latest.tar.gz")"
    echo "$latest" >"$here/VERSION"
    set_var "$here/fetch.sh" wpasupplicant_sha256 "$digest"
    bumped wpa-supplicant "$pinned" "$latest"
    echo "the digest came from this download; check w1.fi's .asc signature by hand"
    ;;
*) fail "this domain has one pin, wpa-supplicant" ;;
esac

# licensing/sources.sh needs no re-pin after this bump. It mirrors
# this domain's source tarball from the domain's own cache, which
# fetch.sh verified against its own digests, so sources.sh holds no
# digest of its own for them.
next_step "make -C .. wpa-supplicant"
