#!/usr/bin/env bash
#
# Vendor k3s, the whole Kubernetes distribution, as one static binary.
#
# k3s makes liken's architecture possible. It compiles containerd,
# kubelet, the API server, scheduler, controller manager, flannel,
# CoreDNS, and a sqlite-backed datastore into one self-contained
# executable. This executable needs nothing from the host beyond a
# kernel. At runtime it also unpacks its own userland (mount,
# iptables, and more) under /var/lib/rancher. One file in the image
# provides the whole service manager.
#
# As with the kernel, this script pins a version (k3s/VERSION, for
# example v1.36.2+k3s1: upstream Kubernetes plus k3s's packaging
# revision). It fetches the published build from the project's GitHub
# releases, and verifies the build against the sha256 manifest
# published beside it.
#
# Usage:
#   k3s/fetch.sh                  fetch the version pinned in k3s/VERSION
#   k3s/fetch.sh v1.36.1+k3s1     fetch a specific version instead
#
# Results land in k3s/dist/<version>/k3s, cached in k3s/cache/.

set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

for tool in curl sha256sum; do
    command -v "$tool" >/dev/null || {
        echo "fetch.sh: missing required tool: $tool" >&2
        exit 1
    }
done

arch="amd64"
version="${1:-$(cat "$here/VERSION")}"

# Release URLs must percent-encode the "+" in k3s version tags.
base="https://github.com/k3s-io/k3s/releases/download/${version//+/%2B}"

cache="$here/cache/$version"
out="$here/dist/$version"
mkdir -p "$cache"

# Each release publishes one sha256 manifest per architecture, covering
# all of its artifacts. The line ending in exactly " k3s" names the
# bare server/agent binary. Its siblings are airgap image bundles,
# which liken does not vendor; a machine pulls its images over the
# network on its first boot instead.
digest="$(curl -fsSL "$base/sha256sum-$arch.txt" | awk '$2 == "k3s" { print $1 }')"
if [[ -z "$digest" ]]; then
    echo "fetch.sh: no k3s binary listed in $base/sha256sum-$arch.txt" >&2
    exit 1
fi

if ! sha256sum --check --status <<<"$digest  $cache/k3s" >/dev/null 2>&1; then
    echo "downloading k3s $version"
    curl -fL --progress-bar -o "$cache/k3s" "$base/k3s"
    sha256sum --check --quiet <<<"$digest  $cache/k3s"
fi

rm -rf "$out"
mkdir -p "$out"
cp "$cache/k3s" "$out/k3s"
chmod +x "$out/k3s"

echo
echo "k3s $version:"
du -sh "$out/k3s"
