#!/usr/bin/env bash
#
# Vendor k3s, the entire Kubernetes distribution, as one static binary.
#
# k3s is the reason liken's architecture works at all: containerd,
# kubelet, the API server, scheduler, controller manager, flannel,
# CoreDNS, and a sqlite-backed datastore, compiled into a single
# self-contained executable with no expectations of the host beyond a
# kernel — it even unpacks its own userland (mount, iptables, and
# friends) under /var/lib/rancher at runtime. We put one file in the
# image and a service manager comes out.
#
# Like the kernel, we pin a version (k3s/VERSION, e.g. v1.36.2+k3s1 —
# upstream Kubernetes plus k3s's packaging revision) and fetch the
# published build from the project's GitHub releases, verifying it
# against the sha256 manifest published beside it.
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

# The "+" in k3s version tags must be percent-encoded in release URLs.
base="https://github.com/k3s-io/k3s/releases/download/${version//+/%2B}"

cache="$here/cache/$version"
out="$here/dist/$version"
mkdir -p "$cache"

# The release publishes one sha256 manifest per architecture covering
# all of its artifacts; the line ending in exactly " k3s" is the bare
# server/agent binary (its siblings are airgap image bundles, which
# liken doesn't vendor — a machine's first boot pulls its images over
# the network).
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

mkdir -p "$out"
cp "$cache/k3s" "$out/k3s"
chmod +x "$out/k3s"

echo
echo "k3s $version:"
du -sh "$out/k3s"
