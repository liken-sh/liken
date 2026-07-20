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
# project's GitHub releases, verified against the sha256 manifest
# published beside it. The version pin (xtables/VERSION) must match the
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
#   xtables/fetch.sh v0.15.1      fetch a specific version instead
#
# Results land in xtables/dist/<version>/bin/, cached in xtables/cache/.

set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

for tool in curl sha256sum tar; do
    command -v "$tool" >/dev/null || {
        echo "fetch.sh: missing required tool: $tool" >&2
        exit 1
    }
done

arch="amd64"
version="${1:-$(cat "$here/VERSION")}"

base="https://github.com/k3s-io/k3s-root/releases/download/$version"
tarball="k3s-root-xtables-$arch.tar"

cache="$here/cache/$version"
out="$here/dist/$version"
mkdir -p "$cache"

# One sha256 manifest per architecture covers all of the release's
# artifacts. This script wants the line for the xtables tarball.
digest="$(curl -fsSL "$base/sha256sum-$arch.txt" | awk -v t="$tarball" '$2 == t { print $1 }')"
if [[ -z "$digest" ]]; then
    echo "fetch.sh: no $tarball listed in $base/sha256sum-$arch.txt" >&2
    exit 1
fi

if ! sha256sum --check --status <<<"$digest  $cache/$tarball" >/dev/null 2>&1; then
    echo "downloading xtables (k3s-root) $version"
    curl -fL --progress-bar -o "$cache/$tarball" "$base/$tarball"
    sha256sum --check --quiet <<<"$digest  $cache/$tarball"
fi

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
