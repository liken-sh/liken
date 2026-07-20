#!/usr/bin/env bash
#
# Vendor mke2fs: the program that creates ext4 filesystems, statically
# linked, for init to run when it claims a blank disk.
#
# Making a filesystem is a userspace job. The kernel can read and
# write ext4 (liken's vendored kernel builds the driver in), but it has
# no code to create one. Laying down superblocks, block groups, inode
# tables, and the journal has always belonged to mke2fs, from the
# e2fsprogs package every distribution ships. liken's image has no
# libc and no shell, so a distro's dynamically-linked mke2fs cannot
# even run here. The binary must be static, like everything else on
# this machine.
#
# There is no pure-Go implementation to use instead. The strongest
# evidence comes from gokrazy, the appliance Linux whose userland is
# otherwise entirely Go: when gokrazy needed to format its permanent
# partition, it bundled a static mke2fs instead of rewriting ext4
# creation. That is exactly the binary vendored here. gokrazy builds it
# from the official e2fsprogs release tarball on kernel.org, with a
# public, reproducible recipe (their repo's Dockerfile: configure with
# LDFLAGS=-static, SOURCE_DATE_EPOCH pinned), and checks the result
# into their repository. This is what makes it fetchable at a pinned
# commit.
#
# gokrazy publishes no checksum manifest, so this file records the
# digest, computed when this pin was chosen. This protects against the
# artifact changing after the pin was set. Anyone who does not want to
# take gokrazy's word for the original bytes can run their Dockerfile
# and compare the results byte for byte. The reproducible build is
# what makes that check possible.
#
# Usage:
#   e2fsprogs/fetch.sh    fetch the version pinned in e2fsprogs/VERSION
#
# Results land in e2fsprogs/dist/<version>/, cached in e2fsprogs/cache/.

set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

for tool in curl sha256sum; do
    command -v "$tool" >/dev/null || {
        echo "fetch.sh: missing required tool: $tool" >&2
        exit 1
    }
done

arch="amd64"
version="$(cat "$here/VERSION")"

# The commit in gokrazy/mkfs that carries this e2fsprogs version, and
# the digest of the binary as vendored. A version bump must update all
# three together: VERSION, the commit, and the digest.
commit="fbb07f9ec5fd85dbed6db8a3f64ef9c55315cc1d"
digest="97beb7dc8af006067586f245d2a4fb63340f81227dab1f9f2d8ba6f3d9e65b1f"

url="https://raw.githubusercontent.com/gokrazy/mkfs/$commit/third_party/e2fsprogs-$version/mke2fs.$arch"

cache="$here/cache/$version"
out="$here/dist/$version"
mkdir -p "$cache"

if ! sha256sum --check --status <<<"$digest  $cache/mke2fs" >/dev/null 2>&1; then
    echo "downloading mke2fs (e2fsprogs $version, via gokrazy/mkfs)"
    curl -fL --progress-bar -o "$cache/mke2fs" "$url"
    sha256sum --check --quiet <<<"$digest  $cache/mke2fs"
fi

rm -rf "$out"
mkdir -p "$out"
cp "$cache/mke2fs" "$out/mke2fs"
chmod +x "$out/mke2fs"

echo
echo "mke2fs (e2fsprogs $version):"
du -sh "$out/mke2fs"
