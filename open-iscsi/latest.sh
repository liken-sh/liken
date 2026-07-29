#!/usr/bin/env bash
#
# Report what this domain pins, and what its four upstreams have now.
#
# This domain builds from source inside a pinned container, so it
# carries four pins: the iSCSI tools themselves, the two static
# libraries the build links, and the image it all builds in. Each one
# is a version and a digest in fetch.sh, except the image, which is
# only a digest, because a tag moves and a digest does not.
#
# The image row compares the pinned digest against whatever the tag
# resolves to today. Alpine rebuilds a release tag when a package in
# it gets a fix, so this row moving is normal and means the base got
# a rebuild, not that a new Alpine came out. Two other domains pin the
# same image, and a bump here does not touch them.
#
# Nothing upstream checksums a GitHub source archive, so those bumps
# compute the digest from their own download. kernel.org publishes a
# sums file, so the kmod bump reads its digest from there.
#
# Usage:
#   open-iscsi/latest.sh                report all four pins
#   open-iscsi/latest.sh --bump         move the iSCSI tools
#   open-iscsi/latest.sh --bump kmod    move one of the other three
#                                       (kmod, libeconf, alpine)

set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# The pins are shell assignments in fetch.sh, so the script reads them
# from there rather than keeping a second copy of each value.
pin_of() { sed -n "s|^$1=\"\([^\"]*\)\".*|\1|p" "$here/fetch.sh"; }

pinned="$(cat "$here/VERSION")"
pinned_kmod="$(pin_of kmod_version)"
pinned_libeconf="$(pin_of libeconf_version)"
# The builder pin is an image reference, and the digest is the part
# of it that moves. The tag beside it is a comment, because a fetch
# that named the tag would not be pinned at all.
pinned_builder="$(sed -n 's|^builder=".*@\(sha256:[^"]*\)".*|\1|p' "$here/fetch.sh")"
builder_tag="$(sed -n 's|^builder=.*# \(.*\)$|\1|p' "$here/fetch.sh")"

newest_tag() {
    git ls-remote --tags --refs "$1" |
        sed "s|.*refs/tags/$2||" |
        grep -E "$3" |
        sort -V | tail -1
}

# A registry names an image by digest, and a tag is a label on top of
# one. Reading the tag's digest takes an anonymous pull token and one
# HEAD request, which keeps this report free of a docker daemon.
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

latest="$(newest_tag https://github.com/open-iscsi/open-iscsi '' '^[0-9]+\.[0-9]+\.[0-9]+$')" || latest=""
latest_kmod="$(curl -fsS --retry 3 https://www.kernel.org/pub/linux/utils/kernel/kmod/ |
    grep -oE 'kmod-[0-9]+\.tar\.xz' | sed -e 's|kmod-||' -e 's|\.tar\.xz||' |
    sort -V | tail -1)" || latest_kmod=""
latest_libeconf="$(newest_tag https://github.com/openSUSE/libeconf v '^[0-9]+\.[0-9]+\.[0-9]+$')" || latest_libeconf=""
latest_builder="$(image_digest "$builder_tag")" || latest_builder=""

# The digest is 71 characters and the table has to stay readable, so
# the rows carry enough of it to tell two apart.
short() { echo "${1#sha256:}" | cut -c1-12; }

printf '%s\t%s\t%s\t%s\n' open-iscsi "$pinned" "${latest:-?}" \
    "newest release tag"
printf '%s\t%s\t%s\t%s\n' '  kmod' "$pinned_kmod" "${latest_kmod:-?}" \
    "libkmod, linked static"
printf '%s\t%s\t%s\t%s\n' '  libeconf' "$pinned_libeconf" "${latest_libeconf:-?}" \
    "libeconf, linked static"
printf '%s\t%s\t%s\t%s\n' '  alpine' \
    "$(short "$pinned_builder")" "$(short "${latest_builder:-?}")" \
    "the $builder_tag builder; nfs-utils and tzdata pin it too"

[[ "${1:-}" == "--bump" ]] || exit 0

# repin <version variable> <digest variable> <new version> <url>:
# download the new source, write both pins, and report where the
# digest came from.
repin() {
    local version_var="$1" digest_var="$2" value="$3" url="$4" digest
    digest="$(curl -fsSL --retry 3 "$url" | sha256sum | cut -d' ' -f1)"
    # The domain's own version lives in VERSION, not in fetch.sh, so
    # that call passes no variable name for it.
    if [[ -n "$version_var" ]]; then
        sed -i "s|^$version_var=\".*\"|$version_var=\"$value\"|" "$here/fetch.sh"
    fi
    sed -i "s|^$digest_var=\".*\"|$digest_var=\"$digest\"|" "$here/fetch.sh"
}

case "${2:-open-iscsi}" in
open-iscsi)
    [[ -n "$latest" ]] || {
        echo "latest.sh: the tag list did not answer" >&2
        exit 1
    }
    [[ "$latest" != "$pinned" ]] || {
        echo "open-iscsi $pinned is current"
        exit 0
    }
    repin "" openiscsi_sha256 "$latest" \
        "https://github.com/open-iscsi/open-iscsi/archive/refs/tags/$latest.tar.gz"
    echo "$latest" >"$here/VERSION"
    echo "open-iscsi: $pinned -> $latest"
    echo "the digest came from this download; nothing upstream signs it"
    ;;
kmod)
    [[ -n "$latest_kmod" ]] || {
        echo "latest.sh: the kmod directory did not answer" >&2
        exit 1
    }
    [[ "$latest_kmod" != "$pinned_kmod" ]] || {
        echo "kmod $pinned_kmod is current"
        exit 0
    }
    # kernel.org signs one sums file per directory, so this digest
    # comes from upstream and not from the download.
    digest="$(curl -fsSL --retry 3 https://www.kernel.org/pub/linux/utils/kernel/kmod/sha256sums.asc |
        awk -v t="kmod-$latest_kmod.tar.xz" '$2 == t { print $1 }')"
    [[ -n "$digest" ]] || {
        echo "latest.sh: no kmod-$latest_kmod.tar.xz in kernel.org's sums file" >&2
        exit 1
    }
    sed -i \
        -e "s|^kmod_version=\".*\"|kmod_version=\"$latest_kmod\"|" \
        -e "s|^kmod_sha256=\".*\"|kmod_sha256=\"$digest\"|" \
        "$here/fetch.sh"
    echo "kmod: $pinned_kmod -> $latest_kmod"
    echo "kernel.org's sums file stands behind these bytes"
    ;;
libeconf)
    [[ -n "$latest_libeconf" ]] || {
        echo "latest.sh: the tag list did not answer" >&2
        exit 1
    }
    [[ "$latest_libeconf" != "$pinned_libeconf" ]] || {
        echo "libeconf $pinned_libeconf is current"
        exit 0
    }
    repin libeconf_version libeconf_sha256 "$latest_libeconf" \
        "https://github.com/openSUSE/libeconf/archive/refs/tags/v$latest_libeconf.tar.gz"
    echo "libeconf: $pinned_libeconf -> $latest_libeconf"
    echo "the digest came from this download; nothing upstream signs it"
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
    echo "nfs-utils and tzdata pin the same image; run their --bump alpine too"
    ;;
*)
    echo "latest.sh: no pin named ${2}; try kmod, libeconf, or alpine" >&2
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
echo "next: make -C .. open-iscsi"
