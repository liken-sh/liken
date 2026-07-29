#!/usr/bin/env bash
#
# Report what this domain pins, and what its three upstreams have now.
#
# This domain builds from source inside a pinned container, the same
# shape as open-iscsi, so it carries three pins: the NFS tools, the
# RPC library they link, and the image they build in.
#
# The image row compares the pinned digest against whatever the tag
# resolves to today. Alpine rebuilds a release tag when a package in
# it gets a fix, so this row moving is normal. Two other domains pin
# the same image, and a bump here does not touch them.
#
# kernel.org publishes a sums file in every release directory, so the
# nfs-utils bump reads its digest from upstream. SourceForge publishes
# none, so the libtirpc bump computes its own.
#
# Usage:
#   nfs-utils/latest.sh                 report all three pins
#   nfs-utils/latest.sh --bump          move the NFS tools
#   nfs-utils/latest.sh --bump libtirpc move one of the other two
#                                       (libtirpc, alpine)

set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

archive="https://www.kernel.org/pub/linux/utils/nfs-utils"

pin_of() { sed -n "s|^$1=\"\([^\"]*\)\".*|\1|p" "$here/fetch.sh"; }

pinned="$(cat "$here/VERSION")"
pinned_libtirpc="$(pin_of libtirpc_version)"
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

# Every release gets its own directory here, so the listing of
# directory names is the list of releases.
latest="$(curl -fsS --retry 3 "$archive/" |
    grep -oE 'href="[0-9]+\.[0-9]+\.[0-9]+/"' |
    sed -e 's|href="||' -e 's|/"$||' |
    sort -V | tail -1)" || latest=""

# SourceForge's file listing is a page of markup, but its per-path RSS
# feed names each file plainly, so the feed is the cheaper read.
latest_libtirpc="$(curl -fsS --retry 3 "https://sourceforge.net/projects/libtirpc/rss?path=/libtirpc" |
    grep -oE 'libtirpc-[0-9]+\.[0-9]+\.[0-9]+\.tar\.bz2' |
    sed -e 's|libtirpc-||' -e 's|\.tar\.bz2||' |
    sort -V | tail -1)" || latest_libtirpc=""

latest_builder="$(image_digest "$builder_tag")" || latest_builder=""

short() { echo "${1#sha256:}" | cut -c1-12; }

printf '%s\t%s\t%s\t%s\n' nfs-utils "$pinned" "${latest:-?}" \
    "newest release on kernel.org"
printf '%s\t%s\t%s\t%s\n' '  libtirpc' "$pinned_libtirpc" "${latest_libtirpc:-?}" \
    "the RPC library, linked static"
printf '%s\t%s\t%s\t%s\n' '  alpine' \
    "$(short "$pinned_builder")" "$(short "${latest_builder:-?}")" \
    "the $builder_tag builder; open-iscsi and tzdata pin it too"

[[ "${1:-}" == "--bump" ]] || exit 0

case "${2:-nfs-utils}" in
nfs-utils)
    [[ -n "$latest" ]] || {
        echo "latest.sh: the nfs-utils directory did not answer" >&2
        exit 1
    }
    [[ "$latest" != "$pinned" ]] || {
        echo "nfs-utils $pinned is current"
        exit 0
    }
    digest="$(curl -fsSL --retry 3 "$archive/$latest/sha256sums.asc" |
        awk -v t="nfs-utils-$latest.tar.xz" '$2 == t { print $1 }')"
    [[ -n "$digest" ]] || {
        echo "latest.sh: no nfs-utils-$latest.tar.xz in that release's sums file" >&2
        exit 1
    }
    echo "$latest" >"$here/VERSION"
    sed -i "s|^nfsutils_sha256=\".*\"|nfsutils_sha256=\"$digest\"|" "$here/fetch.sh"
    echo "nfs-utils: $pinned -> $latest"
    echo "kernel.org's sums file stands behind these bytes"
    ;;
libtirpc)
    [[ -n "$latest_libtirpc" ]] || {
        echo "latest.sh: the libtirpc feed did not answer" >&2
        exit 1
    }
    [[ "$latest_libtirpc" != "$pinned_libtirpc" ]] || {
        echo "libtirpc $pinned_libtirpc is current"
        exit 0
    }
    digest="$(curl -fsSL --retry 3 \
        "https://downloads.sourceforge.net/project/libtirpc/libtirpc/$latest_libtirpc/libtirpc-$latest_libtirpc.tar.bz2" |
        sha256sum | cut -d' ' -f1)"
    sed -i \
        -e "s|^libtirpc_version=\".*\"|libtirpc_version=\"$latest_libtirpc\"|" \
        -e "s|^libtirpc_sha256=\".*\"|libtirpc_sha256=\"$digest\"|" \
        "$here/fetch.sh"
    echo "libtirpc: $pinned_libtirpc -> $latest_libtirpc"
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
    echo "open-iscsi and tzdata pin the same image; run their --bump alpine too"
    ;;
*)
    echo "latest.sh: no pin named ${2}; try libtirpc or alpine" >&2
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
echo "next: make -C .. nfs-utils"
