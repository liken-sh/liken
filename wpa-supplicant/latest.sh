#!/usr/bin/env bash
#
# Reports the two pins this domain carries: the supplicant itself and
# the container image it builds in. A hostap release is a numbered
# tarball on w1.fi, cut every year or two, with a matching hostap_2_N
# tag in the project's git. w1.fi publishes no checksum file, so a
# bump computes the digest from its own download. It does publish a
# detached PGP signature, which this domain does not check; the bump
# says so and names the file to check by hand.
#
# The image row compares the pinned digest against whatever the tag
# resolves to today. Alpine rebuilds a release tag when a package in
# it gets a fix, so this row moving is normal. Three other domains
# pin the same image, and a bump here does not touch them.
#
# Usage:
#   wpa-supplicant/latest.sh                report both pins
#   wpa-supplicant/latest.sh --bump         move the supplicant
#   wpa-supplicant/latest.sh --bump alpine  move the builder image

set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

releases="https://w1.fi/releases"

pinned="$(cat "$here/VERSION")"
# The builder pin is an image reference, and the digest is the part
# of it that moves. The tag beside it is a comment, because a fetch
# that named the tag would not be pinned at all.
pinned_builder="$(sed -n 's|^builder=".*@\(sha256:[^"]*\)".*|\1|p' "$here/fetch.sh")"
builder_tag="$(sed -n 's|^builder=.*# \(.*\)$|\1|p' "$here/fetch.sh")"

image_digest() {
    local token
    token="$(curl -fsS --retry 3 \
        "https://auth.docker.io/token?service=registry.docker.io&scope=repository:library/alpine:pull" |
        sed 's/.*"token":"//;s/".*//')"
    curl -fsSI --retry 3 -H "Authorization: Bearer $token" \
        -H "Accept: application/vnd.oci.image.index.v1+json,application/vnd.docker.distribution.manifest.list.v2+json" \
        "https://registry-1.docker.io/v2/library/alpine/manifests/$1" |
        grep -i '^docker-content-digest:' | tr -d '\r' | awk '{ print $2 }'
}

# hostap publishes one tarball per release in this one directory, so
# the names in the directory listing are the release list.
latest="$(curl -fsS --retry 3 "$releases/" |
    grep -oE 'wpa_supplicant-[0-9]+\.[0-9]+\.tar\.gz' |
    sed -e 's|wpa_supplicant-||' -e 's|\.tar\.gz||' |
    sort -V | tail -1)" || latest=""
latest_builder="$(image_digest "$builder_tag")" || latest_builder=""

short() { echo "${1#sha256:}" | cut -c1-12; }

printf '%s\t%s\t%s\t%s\n' wpa-supplicant "$pinned" "${latest:-?}" \
    "newest release on w1.fi"
printf '%s\t%s\t%s\t%s\n' '  alpine' \
    "$(short "$pinned_builder")" "$(short "${latest_builder:-?}")" \
    "the $builder_tag builder; open-iscsi, nfs-utils, and tzdata pin it too"

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
    digest="$(curl -fsSL --retry 3 "$releases/wpa_supplicant-$latest.tar.gz" |
        sha256sum | cut -d' ' -f1)"
    echo "$latest" >"$here/VERSION"
    sed -i "s|^wpasupplicant_sha256=\".*\"|wpasupplicant_sha256=\"$digest\"|" \
        "$here/fetch.sh"
    echo "wpa-supplicant: $pinned -> $latest"
    echo "the digest came from this download; check w1.fi's .asc signature by hand"
    ;;
alpine)
    [[ -n "$latest_builder" ]] || {
        echo "latest.sh: the registry did not answer" >&2
        exit 1
    }
    [[ "$latest_builder" != "$pinned_builder" ]] || {
        echo "the $builder_tag builder is current"
        exit 0
    }
    sed -i "s|^builder=\".*\"|builder=\"docker.io/library/alpine@$latest_builder\"|" \
        "$here/fetch.sh"
    echo "alpine: $(short "$pinned_builder") -> $(short "$latest_builder")"
    echo "open-iscsi, nfs-utils, and tzdata pin the same image; run their --bump alpine too"
    ;;
*)
    echo "latest.sh: no pin named ${2}; try alpine" >&2
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
