#!/usr/bin/env bash
#
# Report what this domain pins, and the k3s-root release that the
# pinned k3s bundles.
#
# k3s-root is the buildroot recipe that k3s itself uses to build the
# userland it embeds. liken takes the same release, so that the
# iptables binaries on the host and the ones inside k3s come from one
# build. The newest k3s-root tag is the wrong answer: k3s takes a new
# k3s-root in a later patch release, and until then the newest tag
# differs from what k3s carries. Each k3s release names its k3s-root
# as VERSION_ROOT in scripts/version.sh, so this script reads that
# line at the tag in k3s/VERSION. A k3s bump therefore comes first,
# and this domain follows it.
#
# The pin carries the tarball's digest in xtables/pin.toml. Each
# k3s-root release publishes a sha256 manifest per architecture, and
# --bump copies the xtables tarball's digest from it into pin.toml.
#
# The same manifest pins the corresponding source that
# licensing/sources.sh mirrors: k3s-root's recipe at the release's tag,
# the buildroot release that the recipe builds with, and the iptables
# release that buildroot builds. The release names the other two
# through a chain. k3s-root's scripts/download names the buildroot
# release, and buildroot's package/iptables/iptables.mk names the
# iptables release. k3s-root replaces some of buildroot's packages
# with its own, but not iptables. So the two sources are sub-pins of
# this one, and their report rows compare the manifest with what the
# release in VERSION names.
#
# The iptables digest comes from buildroot's package/iptables/iptables.hash
# at the same tag, which is the digest that buildroot checked when it
# built the binaries. GitHub publishes no digest for the archive of a
# tag, and k3s-root downloads buildroot's archive with no checksum, so
# the digests of the two archives come from the bump's own download.
#
# This pin follows another pin, k3s, so it has no channel of its own,
# and every channel column prints `-`. Under the rule in root plan 83
# it is current when it matches the k3s-root release that the pinned
# k3s names.
#
# Usage:
#   xtables/latest.sh          report the pin and the release k3s bundles
#   xtables/latest.sh --bump   write the release k3s bundles into VERSION,
#                              and its tarball and its sources into
#                              pin.toml

set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source-path=SCRIPTDIR source=../../toolchains/pin.sh
source "$here/../../toolchains/pin.sh"

pinned="$(cat "$here/VERSION")"

# The tag carries a +, which a URL path must escape.
k3s="$(cat "$here/../k3s/VERSION")"
latest="$(fetch_text "https://raw.githubusercontent.com/k3s-io/k3s/${k3s/+/%2B}/scripts/version.sh" |
    sed -n 's/^VERSION_ROOT="\(v[0-9.]*\)"$/\1/p')" || latest=""

# buildroot_of <k3s-root tag>: print the buildroot release that the
# k3s-root release builds with.
buildroot_of() {
    fetch_text "https://raw.githubusercontent.com/k3s-io/k3s-root/$1/scripts/download" |
        sed -n 's|.*BUILDROOT_VERSION:=\([^}]*\)}.*|\1|p' | head -1
}

# iptables_of <buildroot tag>: print the iptables release that the
# buildroot release builds.
iptables_of() {
    fetch_text "https://raw.githubusercontent.com/buildroot/buildroot/$1/package/iptables/iptables.mk" |
        sed -n 's|^IPTABLES_VERSION *= *||p' | head -1
}

buildroot_pinned="$(pin_value buildroot-source version)"
iptables_pinned="$(pin_value iptables-source version)"
buildroot_built="$(buildroot_of "$pinned")" || buildroot_built=""
iptables_built=""
[[ -z "$buildroot_built" ]] || iptables_built="$(iptables_of "$buildroot_built")" || iptables_built=""

row xtables "$pinned" "$latest" - - - \
    "follows k3s $k3s: the k3s-root release it builds its userland from"
row '  iptables' "$iptables_pinned" "$iptables_built" - - - \
    "the source of what k3s-root $pinned builds"
row '  buildroot' "$buildroot_pinned" "$buildroot_built" - - - \
    "the source of what k3s-root $pinned builds with"

[[ "${1:-}" == "--bump" ]] || exit 0

[[ -n "$latest" ]] || fail "k3s $k3s names no VERSION_ROOT"

# A release that is current with sources that are not is a manifest
# that someone edited by hand, so the bump writes the sources of the
# pinned release again.
release="$latest"
if [[ "$pinned" == "$latest" ]]; then
    [[ -n "$buildroot_built" && -n "$iptables_built" ]] ||
        fail "the sources of k3s-root $pinned did not answer"
    if [[ "$buildroot_pinned" == "$buildroot_built" && "$iptables_pinned" == "$iptables_built" ]]; then
        echo "xtables $pinned is current"
        exit 0
    fi
fi

# Every digest is read before the first write, so a bump that stops
# leaves the pin as it was.
base="https://github.com/k3s-io/k3s-root/releases/download/$release"
manifest="$base/sha256sum-amd64.txt"
digest="$(fetch_text "$manifest" |
    awk '$2 == "k3s-root-xtables-amd64.tar" { print $1 }')" || digest=""
[[ "$digest" =~ ^[0-9a-f]{64}$ ]] || fail "$manifest lists no xtables tarball"

buildroot="$(buildroot_of "$release")" || buildroot=""
[[ -n "$buildroot" ]] || fail "k3s-root $release names no buildroot release"
iptables="$(iptables_of "$buildroot")" || iptables=""
[[ -n "$iptables" ]] || fail "buildroot $buildroot names no iptables release"
iptables_tarball="iptables-$iptables.tar.xz"
iptables_url="https://www.netfilter.org/pub/iptables/$iptables_tarball"
iptables_hash="https://raw.githubusercontent.com/buildroot/buildroot/$buildroot/package/iptables/iptables.hash"
iptables_digest="$(fetch_text "$iptables_hash" |
    awk -v t="$iptables_tarball" '$1 == "sha256" && $3 == t { print $2 }')" || iptables_digest=""
[[ "$iptables_digest" =~ ^[0-9a-f]{64}$ ]] || fail "$iptables_hash lists no $iptables_tarball"
recipe_url="https://github.com/k3s-io/k3s-root/archive/refs/tags/$release.tar.gz"
recipe_digest="$(digest_of_url "$recipe_url")"
buildroot_url="https://github.com/buildroot/buildroot/archive/refs/tags/$buildroot.tar.gz"
buildroot_digest="$(digest_of_url "$buildroot_url")"

if [[ "$release" != "$pinned" ]]; then
    echo "$release" >"$here/VERSION"
    set_pin k3s-root-xtables "$release" "$base/k3s-root-xtables-amd64.tar" "$digest" \
        "$(github_purl k3s-io/k3s-root "$release")"
fi
set_pin k3s-root-source "$release" "$recipe_url" "$recipe_digest" \
    "$(github_purl k3s-io/k3s-root "$release")"
set_pin iptables-source "$iptables" "$iptables_url" "$iptables_digest" \
    "$(generic_purl iptables "$iptables" "$iptables_url" "$iptables_digest")"
set_pin buildroot-source "$buildroot" "$buildroot_url" "$buildroot_digest" \
    "$(github_purl buildroot/buildroot "$buildroot")"
bumped xtables "$pinned" "$release"
echo "  iptables: $iptables_pinned -> $iptables"
echo "  buildroot: $buildroot_pinned -> $buildroot"
echo "the tarball's digest came from the release's sha256 manifest"
echo "the iptables digest came from buildroot's iptables.hash; the archives' digests came from this bump's download, because GitHub signs no archive"
next_step "make -C .. xtables"
