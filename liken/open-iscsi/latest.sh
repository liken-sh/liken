#!/usr/bin/env bash
#
# Report what this domain pins, and what its three upstreams have now.
#
# This domain builds from source inside a container, so it carries
# three pins: the iSCSI tools themselves and the two static libraries
# the build links. Each one is a version and a digest in fetch.sh.
#
# `liken` builds this domain from source, so its pins have no channel,
# and both channel columns print `-`. Under the rule in root plan 83,
# each pin follows the newest release of its upstream project. The
# image that the build runs in is the `alpine` entry in
# toolchains/images.toml, and toolchains/latest.sh reports it.
#
# Nothing upstream checksums a GitHub source archive, so those bumps
# compute the digest from their own download. kernel.org publishes a
# sums file for kmod that its checksum autosigner clearsigns, so the
# kmod bump reads its digest from there, and only after the signature
# verifies against the autosigner's key:
# trust/keys/kernel-org-digest.sh makes the check, and
# trust/keys/README.md says where the key comes from.
#
# Usage:
#   open-iscsi/latest.sh                report all three pins
#   open-iscsi/latest.sh --bump         move the iSCSI tools
#   open-iscsi/latest.sh --bump kmod    move one of the other two
#                                       (kmod, libeconf)

set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source-path=SCRIPTDIR source=../../toolchains/pin.sh
source "$here/../../toolchains/pin.sh"

pinned="$(cat "$here/VERSION")"
pinned_kmod="$(var_of "$here/fetch.sh" kmod_version)"
pinned_libeconf="$(var_of "$here/fetch.sh" libeconf_version)"

latest="$(newest_tag https://github.com/open-iscsi/open-iscsi '^[0-9]+\.[0-9]+\.[0-9]+$')" || latest=""
latest_kmod="$(fetch_text https://www.kernel.org/pub/linux/utils/kernel/kmod/ |
    grep -oE 'kmod-[0-9]+\.tar\.xz' | sed -e 's|kmod-||' -e 's|\.tar\.xz||' |
    sort -V | tail -1)" || latest_kmod=""
latest_libeconf="$(newest_tag https://github.com/openSUSE/libeconf '^[0-9]+\.[0-9]+\.[0-9]+$' v)" || latest_libeconf=""
row open-iscsi "$pinned" "$latest" - - - "newest release tag"
row '  kmod' "$pinned_kmod" "$latest_kmod" - - - "libkmod, linked static"
row '  libeconf' "$pinned_libeconf" "$latest_libeconf" - - - "libeconf, linked static"

[[ "${1:-}" == "--bump" ]] || exit 0

case "${2:-open-iscsi}" in
open-iscsi)
    bump_needed open-iscsi "$pinned" "$latest" || exit 0
    digest="$(digest_of_url "https://github.com/open-iscsi/open-iscsi/archive/refs/tags/$latest.tar.gz")"
    set_var "$here/fetch.sh" openiscsi_sha256 "$digest"
    echo "$latest" >"$here/VERSION"
    bumped open-iscsi "$pinned" "$latest"
    echo "the digest came from this download; nothing upstream signs it"
    ;;
kmod)
    bump_needed kmod "$pinned_kmod" "$latest_kmod" || exit 0
    digest="$("$here/../../trust/keys/kernel-org-digest.sh" \
        https://www.kernel.org/pub/linux/utils/kernel/kmod/sha256sums.asc "kmod-$latest_kmod.tar.xz")"
    set_var "$here/fetch.sh" kmod_version "$latest_kmod"
    set_var "$here/fetch.sh" kmod_sha256 "$digest"
    bumped kmod "$pinned_kmod" "$latest_kmod"
    echo "the digest came from sha256sums.asc, signed by kernel.org's checksum autosigner"
    ;;
libeconf)
    bump_needed libeconf "$pinned_libeconf" "$latest_libeconf" || exit 0
    digest="$(digest_of_url "https://github.com/openSUSE/libeconf/archive/refs/tags/v$latest_libeconf.tar.gz")"
    set_var "$here/fetch.sh" libeconf_version "$latest_libeconf"
    set_var "$here/fetch.sh" libeconf_sha256 "$digest"
    bumped libeconf "$pinned_libeconf" "$latest_libeconf"
    echo "the digest came from this download; nothing upstream signs it"
    ;;
*) fail "no pin named ${2}; try kmod or libeconf" ;;
esac

# licensing/sources.sh needs no re-pin after this bump. It mirrors
# this domain's source tarballs from the domain's own cache, which
# fetch.sh verified against its own digests, so sources.sh holds no
# digest of its own for them.
next_step "make -C .. open-iscsi"
