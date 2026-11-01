#!/usr/bin/env bash
#
# Vendor the netfilter userspace: static xtables binaries for the
# iptables rules that kube-proxy, flannel, and the network policy
# controller write.
#
# The kernel enforces netfilter rules, but userspace programs install
# them, and Kubernetes networking runs those programs constantly. liken
# ships them itself, at /sbin, so the machine's packet filter does not
# depend on any other component to unpack its internals first.
#
# The binaries come from k3s-root, the buildroot project that produces
# k3s's own bundled userland. As a result, the bytes on liken's PATH
# are the same bytes k3s carries. This script fetches them the same way
# as every other vendored input: a pinned version, fetched from the
# project's GitHub releases, verified against the digest in
# xtables/pin.toml, which xtables/latest.sh --bump copies from the
# sha256 manifest published beside it. The version pin (xtables/VERSION) must match the
# VERSION_ROOT in the vendored k3s release's scripts/version.sh. This
# keeps the two copies of xtables on the machine in agreement.
#
# One artifact in the tarball matters here: xtables-legacy-multi, a
# multi-call binary that behaves as whichever tool the caller invokes
# it as (iptables, iptables-save, and so on), the same approach
# busybox uses. The image build stages it at /sbin under each of its
# names. This script takes the legacy variant deliberately: it matches
# the iptable_* kernel modules the image ships. The tarball's own bare
# "iptables" names point at a legacy-vs-nftables detection script that
# needs /bin/sh, and this machine has no shell, so liken makes that
# choice statically instead.
#
# Usage:
#   xtables/fetch.sh              fetch the version pinned in xtables/VERSION
#
# Results land in xtables/dist/<version>/bin/, cached in xtables/cache/.

set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source-path=SCRIPTDIR source=../../toolchains/pin.sh
source "$here/../../toolchains/pin.sh"

require_tools sha256sum tar

arch="amd64"
version="$(cat "$here/VERSION")"
tarball="k3s-root-xtables-$arch.tar"

cache="$here/cache/$version"
out="$here/dist/$version"

fetch_pinned --resume k3s-root-xtables "$version" "$cache/$tarball"

rm -rf "$out"
mkdir -p "$out"
tar -xf "$cache/$tarball" -C "$out"

# tar restores the archive's own timestamps, which predate this
# script. Without this fix, Make would always judge the extracted
# binaries older than their prerequisites, and re-run this fetch on
# every build. The extraction time is the correct timestamp for a
# vendored artifact.
find "$out" -exec touch {} +

echo
echo "xtables (k3s-root) $version:"
du -sh "$out/bin/xtables-legacy-multi"
