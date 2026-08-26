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
# workflow mirrors this verified tarball to the channel
# (licensing/sources.sh).
#
# This domain carries a second pin. cfg80211 declares regulatory.db
# and regulatory.db.p7s, the linux-firmware release carries neither,
# and the wireless-regdb project is where both come from. The kernel
# needs the file to lift the world-default limits: until it loads,
# most 5GHz channels stay closed to a radio. The .p7s is the detached
# signature the kernel checks against its built-in key. The file
# belongs to this domain because it is a name the module tree
# declares, exactly like every blob above.
#
# Usage:
#   linux-firmware/fetch.sh    fetch the versions pinned in VERSION
#                              and in regdb_version below
#
# The tarballs land in linux-firmware/cache/, and the extracted trees
# in linux-firmware/cache/<version>/ and
# linux-firmware/cache/wireless-regdb-<version>/. derive.sh reads both
# trees; it never reaches the network.

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

digest="ac17c34fe73756926a961fbafadf8d8f07a3bd2dd2f4ea31a0fb5d50c714a49a"

# The nested pin. wireless-regdb names its releases for the date, the
# way linux-firmware does, and kernel.org publishes one clearsigned
# sha256sums.asc for the directory. linux-firmware/latest.sh --bump
# wireless-regdb moves this pair. The two pins move independently,
# because the two upstreams cut releases on their own schedules.
regdb_version="2026.05.30"
regdb_digest="8a27bfc081bafed8c24dd70fab0d96f098e5a0bfcd08d3da672595f225ab8993"

cache="$here/cache"
mkdir -p "$cache"

if ! sha256sum --check --status <<<"$digest  $cache/$tarball" >/dev/null 2>&1; then
    echo "downloading $tarball"
    # This tarball is about half a gigabyte, and a long HTTP/2 stream
    # sometimes breaks partway through. A plain retry would start
    # over and can fail the same way forever, so -C - resumes each
    # attempt from the bytes already on disk and repeated breaks
    # still converge on a complete file. The checksum below is what
    # decides whether the assembled bytes are good.
    curl -fL --retry 5 --retry-all-errors --retry-delay 5 -C - \
        --progress-bar -o "$cache/$tarball" "$url"
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

# The regulatory database arrives as a second tarball because its
# project is not part of the linux-firmware tree; the release above
# simply does not contain it.
regdb_tarball="wireless-regdb-$regdb_version.tar.xz"
regdb_url="https://mirrors.edge.kernel.org/pub/software/network/wireless-regdb/$regdb_tarball"

if ! sha256sum --check --status <<<"$regdb_digest  $cache/$regdb_tarball" >/dev/null 2>&1; then
    echo "downloading $regdb_tarball"
    curl -fL --retry 5 --retry-all-errors --retry-delay 5 \
        --progress-bar -o "$cache/$regdb_tarball" "$regdb_url"
    sha256sum --check --quiet <<<"$regdb_digest  $cache/$regdb_tarball"
fi

# regulatory.db is the artifact this pin exists for, so its presence
# is what says the extraction finished.
regdb_tree="$cache/wireless-regdb-$regdb_version"
if [[ ! -f "$regdb_tree/regulatory.db" ]]; then
    echo "extracting $regdb_tarball"
    rm -rf "$regdb_tree"
    mkdir -p "$regdb_tree"
    tar -xf "$cache/$regdb_tarball" -C "$regdb_tree" --strip-components=1
fi

echo "wireless-regdb $regdb_version: $(find "$regdb_tree" -type f | wc -l) files"
