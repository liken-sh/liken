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
# releases, and verifies the build against the digest pinned below,
# which k3s/latest.sh --bump copies from the sha256 manifest published
# beside the build.
#
# Usage:
#   k3s/fetch.sh                  fetch the version pinned in k3s/VERSION
#   k3s/fetch.sh v1.36.1+k3s1     fetch another version that has a
#                                 digest pinned below
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

version="${1:-$(cat "$here/VERSION")}"

# Release URLs must percent-encode the "+" in k3s version tags.
base="https://github.com/k3s-io/k3s/releases/download/${version//+/%2B}"

cache="$here/cache/$version"
out="$here/dist/$version"
mkdir -p "$cache"

# The digest of the bare k3s binary, pinned beside the version it belongs to.
# k3s/latest.sh --bump copies it from the sha256 manifest that the
# release publishes, so a fetch never reads a digest from upstream, and
# a file that changes upstream after the bump fails the check.
declare -A k3s_sha256=(
    ["v1.37.1+k3s1"]="92b94582eb7b34a8cf532e6006842b9ed215a6783cf56a3dd6271fd35243b2c0"
)
digest="${k3s_sha256[$version]:-}"
if [[ -z "$digest" ]]; then
    echo "fetch.sh: no digest pinned for k3s $version; k3s/latest.sh --bump writes it" >&2
    exit 1
fi

if ! sha256sum --check --status <<<"$digest  $cache/k3s" >/dev/null 2>&1; then
    echo "downloading k3s $version"
    # The k3s binary is about 80 MB, and a long stream sometimes
    # breaks partway through. -C - resumes each retry from the bytes
    # already on disk, so repeated breaks still converge on a
    # complete file. The checksum below decides whether the
    # assembled bytes are good.
    curl -fL --retry 5 --retry-all-errors --retry-delay 5 -C - \
        --progress-bar -o "$cache/k3s" "$base/k3s"
    sha256sum --check --quiet <<<"$digest  $cache/k3s"
fi

rm -rf "$out"
mkdir -p "$out"
cp "$cache/k3s" "$out/k3s"
chmod +x "$out/k3s"

echo
echo "k3s $version:"
du -sh "$out/k3s"
