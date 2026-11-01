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
# checksum list, but linux-firmware/pin.toml records the digest, the
# same arrangement as every pin: a version bump updates VERSION and the
# manifest together, and the fetch fails with an error when they
# disagree.
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
#                              and in the wireless-regdb entry of
#                              pin.toml
#
# The tarballs land in linux-firmware/cache/, and the extracted trees
# in linux-firmware/cache/<version>/ and
# linux-firmware/cache/wireless-regdb-<version>/. derive.sh reads both
# trees; it never reaches the network.

set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source-path=SCRIPTDIR source=../../toolchains/pin.sh
source "$here/../../toolchains/pin.sh"

require_tools sha256sum tar xz

version="$(cat "$here/VERSION")"
tarball="linux-firmware-$version.tar.xz"

# The nested pin. wireless-regdb names its releases for the date, the
# way linux-firmware does, and kernel.org publishes one clearsigned
# sha256sums.asc for the directory. linux-firmware/latest.sh --bump
# wireless-regdb moves its entry in pin.toml, which is the one place
# that names its version. The two pins move independently, because the
# two upstreams cut releases on their own schedules.
regdb_version="$(pin_value wireless-regdb version)"

cache="$here/cache"

# This tarball is large, and a long HTTP/2 stream sometimes breaks
# partway through. A plain retry would start over and can fail the
# same way forever, so each retry resumes from the bytes already on
# disk.
fetch_pinned --resume linux-firmware "$version" "$cache/$tarball"

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

fetch_pinned wireless-regdb "$regdb_version" "$cache/$regdb_tarball"

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
