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
# releases, and verifies the build against the digest in k3s/pin.toml,
# which k3s/latest.sh --bump copies from the sha256 manifest published
# beside the build.
#
# Usage:
#   k3s/fetch.sh                  fetch the version pinned in k3s/VERSION
#
# Results land in k3s/dist/<version>/k3s, cached in k3s/cache/.

set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source-path=SCRIPTDIR source=../../toolchains/pin.sh
source "$here/../../toolchains/pin.sh"

require_tools sha256sum

version="$(cat "$here/VERSION")"

cache="$here/cache/$version"
out="$here/dist/$version"

# The k3s binary is large, and a long stream sometimes breaks partway
# through, so each retry resumes from the bytes already on disk.
fetch_pinned --resume k3s "$version" "$cache/k3s"

rm -rf "$out"
mkdir -p "$out"
cp "$cache/k3s" "$out/k3s"
chmod +x "$out/k3s"

echo
echo "k3s $version:"
du -sh "$out/k3s"
