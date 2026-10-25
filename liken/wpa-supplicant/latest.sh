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

releases="https://w1.fi/releases"

pinned="$(cat "$here/VERSION")"
# hostap publishes one tarball per release in this one directory, so
# the names in the directory listing are the release list.
latest="$(curl -fsS --retry 3 --max-time 60 "$releases/" |
    grep -oE 'wpa_supplicant-[0-9]+\.[0-9]+\.tar\.gz' |
    sed -e 's|wpa_supplicant-||' -e 's|\.tar\.gz||' |
    sort -V | tail -1)" || latest=""
printf '%s\t%s\t%s\t%s\t%s\t%s\t%s\n' wpa-supplicant "$pinned" "${latest:-?}" - - - \
    "newest release on w1.fi"

[[ "${1:-}" == "--bump" ]] || exit 0

case "${2:-wpa-supplicant}" in
wpa-supplicant)
    [[ -n "$latest" ]] || {
        echo "latest.sh: w1.fi did not answer" >&2
        exit 1
    }
    [[ "$latest" != "$pinned" ]] || {
        echo "wpa_supplicant $pinned is current"
        exit 0
    }
    digest="$(curl -fsSL --retry 3 --max-time 300 "$releases/wpa_supplicant-$latest.tar.gz" |
        sha256sum | cut -d' ' -f1)"
    echo "$latest" >"$here/VERSION"
    sed -i "s|^wpasupplicant_sha256=\".*\"|wpasupplicant_sha256=\"$digest\"|" \
        "$here/fetch.sh"
    echo "wpa-supplicant: $pinned -> $latest"
    echo "the digest came from this download; check w1.fi's .asc signature by hand"
    ;;
*)
    echo "latest.sh: this domain has one pin, wpa-supplicant" >&2
    exit 1
    ;;
esac

# A refused re-pin leaves the domain pin moved and the mirror
# behind it, which is a release that cannot serve its own
# sources. Say that plainly, because the fix is a hand edit.
"$here/../licensing/sources.sh" --repin || {
    echo "the pin moved; licensing/sources.sh still needs a hand edit" >&2
    exit 1
}
echo "next: make -C .. wpa-supplicant"
