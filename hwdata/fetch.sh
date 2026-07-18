#!/usr/bin/env bash
#
# Vendor the PCI naming database: the file that turns "1af4:1050"
# into "Red Hat, Inc. Virtio 1.0 GPU".
#
# PCI devices carry only numeric IDs; the names every tool prints
# come from pci.ids, a community-maintained compilation that the
# hwdata project snapshots in versioned releases. liken ships it so
# the unclaimed-hardware report (the hardware package) can name
# devices the way an operator knows them. USB devices need no
# database — they carry their manufacturer and product strings in
# the hardware itself.
#
# The pin is an hwdata release tag (hwdata/VERSION), and the fetch
# takes just the one file, at that tag, from hwdata's repository.
# hwdata publishes no checksum manifest, so the digest is recorded
# here, the same arrangement as the mke2fs vendoring: a version bump
# means updating VERSION and the digest together, and the fetch
# fails loudly when they disagree.
#
# pci.ids is dual-licensed (GPL-2-or-later or 3-clause BSD); liken
# redistributes it under the BSD license, which asks for notices,
# not source — and the file is its own source anyway, the same way
# the CA bundle is. The licensing domain carries its notice.
#
# Usage:
#   hwdata/fetch.sh              fetch the version pinned in hwdata/VERSION
#
# Results land in hwdata/dist/<version>/pci.ids, cached in
# hwdata/cache/.

set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

for tool in curl sha256sum; do
    command -v "$tool" >/dev/null || {
        echo "fetch.sh: missing required tool: $tool" >&2
        exit 1
    }
done

version="$(cat "$here/VERSION")"
url="https://raw.githubusercontent.com/vcrhonek/hwdata/$version/pci.ids"

digest="e7973676afbe298577ce21f61ad615ca057c32163fbdb90e1cfb6eaa23e20731"

cache="$here/cache/$version"
out="$here/dist/$version"
mkdir -p "$cache"

if ! sha256sum --check --status <<<"$digest  $cache/pci.ids" >/dev/null 2>&1; then
    echo "downloading pci.ids $version"
    curl -fL --progress-bar -o "$cache/pci.ids" "$url"
    sha256sum --check --quiet <<<"$digest  $cache/pci.ids"
fi

rm -rf "$out"
mkdir -p "$out"
cp "$cache/pci.ids" "$out/pci.ids"

echo
echo "pci.ids $version:"
grep -c "^[0-9a-f]" "$out/pci.ids" | xargs -I{} echo "{} vendors"
