#!/usr/bin/env bash
#
# Vendor the netfilter userspace: static xtables binaries for the
# iptables rules kube-proxy, flannel, and the network policy controller
# write.
#
# The kernel enforces netfilter rules, but userspace programs install
# them, and Kubernetes networking execs those programs constantly.
# liken ships them itself, at /sbin, so the machine's packet filter
# doesn't depend on any other component unpacking its internals first.
#
# The binaries come from k3s-root, the buildroot project that produces
# k3s's own bundled userland, so the bytes on our PATH are the same
# ones k3s carries. They are delivered the same way as every other
# vendored input: a pinned version, fetched from the project's GitHub
# releases, verified against the sha256 manifest published beside it.
# The version pin (xtables/VERSION) must match the VERSION_ROOT in the
# vendored k3s release's scripts/version.sh, so the two copies of
# xtables on the machine can never disagree.
#
# One artifact in the tarball matters to us: xtables-legacy-multi, a
# multi-call binary that behaves as whichever tool it's invoked as
# (iptables, iptables-save, ...), the same approach busybox uses. The
# image build stages it at /sbin under each of its names. We take the
# legacy variant deliberately: it matches the iptable_* kernel modules
# the image ships. The tarball's own bare "iptables" names point at a
# legacy-vs-nftables detection script that needs /bin/sh, and this
# machine has no shell, so liken makes that decision statically
# instead.
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
# artifacts; we want the line for the xtables tarball.
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

echo
echo "xtables (k3s-root) $version:"
du -sh "$out/bin/xtables-legacy-multi"
