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
# sums file, so the kmod bump reads its digest from there.
#
# Usage:
#   open-iscsi/latest.sh                report all three pins
#   open-iscsi/latest.sh --bump         move the iSCSI tools
#   open-iscsi/latest.sh --bump kmod    move one of the other two
#                                       (kmod, libeconf)

set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# The pins are shell assignments in fetch.sh, so the script reads them
# from there rather than keeping a second copy of each value.
pin_of() { sed -n "s|^$1=\"\([^\"]*\)\".*|\1|p" "$here/fetch.sh"; }

pinned="$(cat "$here/VERSION")"
pinned_kmod="$(pin_of kmod_version)"
pinned_libeconf="$(pin_of libeconf_version)"
newest_tag() {
    git ls-remote --tags --refs "$1" |
        sed "s|.*refs/tags/$2||" |
        grep -E "$3" |
        sort -V | tail -1
}

latest="$(newest_tag https://github.com/open-iscsi/open-iscsi '' '^[0-9]+\.[0-9]+\.[0-9]+$')" || latest=""
latest_kmod="$(curl -fsS --retry 3 --max-time 60 https://www.kernel.org/pub/linux/utils/kernel/kmod/ |
    grep -oE 'kmod-[0-9]+\.tar\.xz' | sed -e 's|kmod-||' -e 's|\.tar\.xz||' |
    sort -V | tail -1)" || latest_kmod=""
latest_libeconf="$(newest_tag https://github.com/openSUSE/libeconf v '^[0-9]+\.[0-9]+\.[0-9]+$')" || latest_libeconf=""
printf '%s\t%s\t%s\t%s\t%s\t%s\t%s\n' open-iscsi "$pinned" "${latest:-?}" - - - \
    "newest release tag"
printf '%s\t%s\t%s\t%s\t%s\t%s\t%s\n' '  kmod' "$pinned_kmod" "${latest_kmod:-?}" - - - \
    "libkmod, linked static"
printf '%s\t%s\t%s\t%s\t%s\t%s\t%s\n' '  libeconf' "$pinned_libeconf" "${latest_libeconf:-?}" - - - \
    "libeconf, linked static"

[[ "${1:-}" == "--bump" ]] || exit 0

# repin <version variable> <digest variable> <new version> <url>:
# download the new source, write both pins, and report where the
# digest came from.
repin() {
    local version_var="$1" digest_var="$2" value="$3" url="$4" digest
    digest="$(curl -fsSL --retry 3 --max-time 300 "$url" | sha256sum | cut -d' ' -f1)"
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
    digest="$(curl -fsSL --retry 3 --max-time 60 https://www.kernel.org/pub/linux/utils/kernel/kmod/sha256sums.asc |
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
*)
    echo "latest.sh: no pin named ${2}; try kmod or libeconf" >&2
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
