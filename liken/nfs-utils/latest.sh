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
# nfs-utils bump reads its digest from upstream. kernel.org's checksum
# autosigner clearsigns the sums file, and the bump copies a digest
# only from a sums file whose signature verifies against the
# autosigner's key: trust/keys/kernel-org-digest.sh makes the check,
# and trust/keys/README.md says where the key comes from. SourceForge
# publishes no sums file for libtirpc, so the libtirpc bump computes
# its own digest.
#
# Usage:
#   nfs-utils/latest.sh                 report both pins
#   nfs-utils/latest.sh --bump          move the NFS tools
#   nfs-utils/latest.sh --bump libtirpc move the RPC library

set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source-path=SCRIPTDIR source=../../toolchains/pin.sh
source "$here/../../toolchains/pin.sh"

archive="https://www.kernel.org/pub/linux/utils/nfs-utils"

pinned="$(cat "$here/VERSION")"
pinned_libtirpc="$(var_of "$here/fetch.sh" libtirpc_version)"
# Every release gets its own directory here, so the listing of
# directory names is the list of releases.
latest="$(fetch_text "$archive/" |
    grep -oE 'href="[0-9]+\.[0-9]+\.[0-9]+/"' |
    sed -e 's|href="||' -e 's|/"$||' |
    sort -V | tail -1)" || latest=""

# SourceForge's file listing is a page of markup, but its per-path RSS
# feed names each file plainly, so the feed is the cheaper read.
latest_libtirpc="$(fetch_text "https://sourceforge.net/projects/libtirpc/rss?path=/libtirpc" |
    grep -oE 'libtirpc-[0-9]+\.[0-9]+\.[0-9]+\.tar\.bz2' |
    sed -e 's|libtirpc-||' -e 's|\.tar\.bz2||' |
    sort -V | tail -1)" || latest_libtirpc=""

row nfs-utils "$pinned" "$latest" - - - "newest release on kernel.org"
row '  libtirpc' "$pinned_libtirpc" "$latest_libtirpc" - - - "the RPC library, linked static"

[[ "${1:-}" == "--bump" ]] || exit 0

case "${2:-nfs-utils}" in
nfs-utils)
    bump_needed nfs-utils "$pinned" "$latest" || exit 0
    digest="$("$here/../../trust/keys/kernel-org-digest.sh" \
        "$archive/$latest/sha256sums.asc" "nfs-utils-$latest.tar.xz")"
    echo "$latest" >"$here/VERSION"
    set_var "$here/fetch.sh" nfsutils_sha256 "$digest"
    bumped nfs-utils "$pinned" "$latest"
    echo "the digest came from sha256sums.asc, signed by kernel.org's checksum autosigner"
    ;;
libtirpc)
    bump_needed libtirpc "$pinned_libtirpc" "$latest_libtirpc" || exit 0
    digest="$(digest_of_url "https://downloads.sourceforge.net/project/libtirpc/libtirpc/$latest_libtirpc/libtirpc-$latest_libtirpc.tar.bz2")"
    set_var "$here/fetch.sh" libtirpc_version "$latest_libtirpc"
    set_var "$here/fetch.sh" libtirpc_sha256 "$digest"
    bumped libtirpc "$pinned_libtirpc" "$latest_libtirpc"
    echo "the digest came from this download; nothing upstream signs it"
    ;;
*) fail "no pin named ${2}; try libtirpc" ;;
esac

# licensing/sources.sh needs no re-pin after this bump. It mirrors
# this domain's source tarballs from the domain's own cache, which
# fetch.sh verified against its own digests, so sources.sh holds no
# digest of its own for them.
next_step "make -C .. nfs-utils"
