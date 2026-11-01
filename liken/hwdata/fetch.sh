#!/usr/bin/env bash
#
# Vendor the PCI naming database: the file that turns "1af4:1050"
# into "Red Hat, Inc. Virtio 1.0 GPU".
#
# PCI devices carry only numeric IDs. The names every tool prints come
# from pci.ids, a community-maintained compilation that the hwdata
# project snapshots in versioned releases. liken ships it so the
# unclaimed-hardware report (the hardware package) can name devices
# the way an operator knows them. USB devices need no database. They
# carry their manufacturer and product strings in the hardware itself.
#
# The pin is an hwdata release tag (hwdata/VERSION), and this fetch
# takes just the one file, at that tag, from hwdata's repository.
# hwdata publishes no checksum manifest, so this file records the
# digest, the same arrangement as the mke2fs vendoring: a version bump
# must update VERSION and the digest together, and the fetch fails
# with an error when they disagree.
#
# pci.ids is dual-licensed (GPL-2-or-later or 3-clause BSD). liken
# redistributes it under the BSD license, which asks for notices, not
# source. The file is its own source anyway, the same as the CA
# bundle. The licensing domain carries its notice.
#
# Usage:
#   hwdata/fetch.sh              fetch the version pinned in hwdata/VERSION
#
# Results land in hwdata/dist/<version>/pci.ids, cached in
# hwdata/cache/.

set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source-path=SCRIPTDIR source=../../toolchains/pin.sh
source "$here/../../toolchains/pin.sh"

require_tools sha256sum

version="$(cat "$here/VERSION")"
url="https://raw.githubusercontent.com/vcrhonek/hwdata/$version/pci.ids"

digest="4f020f031d1f2d011b2a656a191519a056cb9744229551aed2cf7f90dfc2e18a"

cache="$here/cache/$version"
out="$here/dist/$version"

fetch_verified --resume "$url" "$digest" "$cache/pci.ids"

rm -rf "$out"
mkdir -p "$out"
cp "$cache/pci.ids" "$out/pci.ids"

echo
echo "pci.ids $version:"
grep -c "^[0-9a-f]" "$out/pci.ids" | xargs -I{} echo "{} vendors"
