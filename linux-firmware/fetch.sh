#!/usr/bin/env bash
#
# Vendor the linux-firmware release: the blobs that kernel drivers
# load from /lib/firmware at probe time.
#
# Many devices are not complete without a program that the driver
# must hand them before they work. A NIC that needs firmware will not
# link without its blob, and a machine whose network needs firmware
# that the OS did not ship never reaches its cluster. The kernel
# project collects these vendor blobs in the linux-firmware
# repository and snapshots it in dated releases.
#
# The pin is a release date (linux-firmware/VERSION), and this fetch
# takes the release tarball from kernel.org. kernel.org publishes a
# checksum list, but this file records the digest directly, the same
# arrangement as the hwdata vendoring: a version bump must update
# VERSION and the digest together, and the fetch fails with an error
# when they disagree.
#
# The tarball also serves the licensing domain. Some blobs in the
# tree carry the GPL, and the GPL requires a source offer. The
# tarball is the source form that upstream publishes: for most blobs
# the blob is its own source, and for the GPL blobs whose source
# exists, that source lives in this same tree. So the release
# workflow mirrors this one verified tarball to the channel
# (licensing/sources.sh), and this fetch is the only download.
#
# Usage:
#   linux-firmware/fetch.sh    fetch the version pinned in VERSION
#
# The tarball lands in linux-firmware/cache/, and the extracted tree
# in linux-firmware/cache/<version>/. derive.sh reads the tree; it
# never reaches the network.

set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

for tool in curl sha256sum tar xz; do
    command -v "$tool" >/dev/null || {
        echo "fetch.sh: missing required tool: $tool" >&2
        exit 1
    }
done

version="$(cat "$here/VERSION")"
tarball="linux-firmware-$version.tar.xz"
url="https://cdn.kernel.org/pub/linux/kernel/firmware/$tarball"

digest="2b9d8a358e76eb766588609135e53fa548b902c551daae33ee32f26f25e60dbb"

cache="$here/cache"
mkdir -p "$cache"

if ! sha256sum --check --status <<<"$digest  $cache/$tarball" >/dev/null 2>&1; then
    echo "downloading $tarball"
    curl -fL --progress-bar -o "$cache/$tarball" "$url"
    sha256sum --check --quiet <<<"$digest  $cache/$tarball"
fi

# The extracted tree is large (about 1.9 GB), so extraction is
# skipped when the tree for this version is already present. The
# marker is the WHENCE file, the manifest that every release carries.
tree="$cache/$version"
if [[ ! -f "$tree/WHENCE" ]]; then
    echo "extracting $tarball"
    rm -rf "$tree"
    mkdir -p "$tree"
    tar -xf "$cache/$tarball" -C "$tree" --strip-components=1
fi

echo "linux-firmware $version: $(find "$tree" -type f | wc -l) files"
