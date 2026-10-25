#!/usr/bin/env bash
#
# Report what this domain pins, and what its two upstreams have now.
#
# This domain builds from source inside a container, the same shape
# as open-iscsi, so it carries two pins: the NFS tools and the RPC
# library they link.
#
# `liken` builds this domain from source, so its pins have no channel,
# and both channel columns print `-`. Under the rule in root plan 83,
# each pin follows the newest release of its upstream project. The
# image that the build runs in is the `alpine` entry in
# toolchains/images.toml, and toolchains/latest.sh reports it.
#
# kernel.org publishes a sums file in every release directory, so the
# nfs-utils bump reads its digest from upstream. SourceForge publishes
# none, so the libtirpc bump computes its own.
#
# Usage:
#   nfs-utils/latest.sh                 report both pins
#   nfs-utils/latest.sh --bump          move the NFS tools
#   nfs-utils/latest.sh --bump libtirpc move the RPC library

set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

archive="https://www.kernel.org/pub/linux/utils/nfs-utils"

pin_of() { sed -n "s|^$1=\"\([^\"]*\)\".*|\1|p" "$here/fetch.sh"; }

pinned="$(cat "$here/VERSION")"
pinned_libtirpc="$(pin_of libtirpc_version)"
# Every release gets its own directory here, so the listing of
# directory names is the list of releases.
latest="$(curl -fsS --retry 3 --max-time 60 "$archive/" |
    grep -oE 'href="[0-9]+\.[0-9]+\.[0-9]+/"' |
    sed -e 's|href="||' -e 's|/"$||' |
    sort -V | tail -1)" || latest=""

# SourceForge's file listing is a page of markup, but its per-path RSS
# feed names each file plainly, so the feed is the cheaper read.
latest_libtirpc="$(curl -fsS --retry 3 --max-time 60 "https://sourceforge.net/projects/libtirpc/rss?path=/libtirpc" |
    grep -oE 'libtirpc-[0-9]+\.[0-9]+\.[0-9]+\.tar\.bz2' |
    sed -e 's|libtirpc-||' -e 's|\.tar\.bz2||' |
    sort -V | tail -1)" || latest_libtirpc=""

printf '%s\t%s\t%s\t%s\t%s\t%s\t%s\n' nfs-utils "$pinned" "${latest:-?}" - - - \
    "newest release on kernel.org"
printf '%s\t%s\t%s\t%s\t%s\t%s\t%s\n' '  libtirpc' "$pinned_libtirpc" "${latest_libtirpc:-?}" - - - \
    "the RPC library, linked static"

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
    digest="$(curl -fsSL --retry 3 --max-time 60 "$archive/$latest/sha256sums.asc" |
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
    digest="$(curl -fsSL --retry 3 --max-time 300 \
        "https://downloads.sourceforge.net/project/libtirpc/libtirpc/$latest_libtirpc/libtirpc-$latest_libtirpc.tar.bz2" |
        sha256sum | cut -d' ' -f1)"
    sed -i \
        -e "s|^libtirpc_version=\".*\"|libtirpc_version=\"$latest_libtirpc\"|" \
        -e "s|^libtirpc_sha256=\".*\"|libtirpc_sha256=\"$digest\"|" \
        "$here/fetch.sh"
    echo "libtirpc: $pinned_libtirpc -> $latest_libtirpc"
    echo "the digest came from this download; nothing upstream signs it"
    ;;
*)
    echo "latest.sh: no pin named ${2}; try libtirpc" >&2
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
