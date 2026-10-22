#!/usr/bin/env bash
#
# Fetch the lab's UEFI firmware: OVMF, TianoCore edk2 built for QEMU
# guests, from Debian's ovmf-generic package.
#
# The lab pins its own OVMF instead of taking the host's, because the
# firmware decides whether a guest survives a warm reset. edk2 releases
# from 2025.08 through 2025.11 read an IGVM memory map from a fixed
# page of guest RAM, 0x812000, and accept it when its first entry
# holds a nonzero page count. A cold start leaves that page zero. A
# warm reset leaves whatever the kernel stored there, and the firmware
# can read that data as RAM that ends at a wrapped 64-bit address. It
# then places the 64-bit PCI aperture outside the address space, and
# PciHostBridgeDxe loops forever before the boot manager runs. edk2
# commit e4f99a8554 reserves the page, so the kernel never writes it.
# liken/plans/88-disruptions.md records the hang and the evidence.
#
# The pin is a Debian package version (firmware/VERSION). The package
# leaves Debian's pool when a newer build replaces it, so this fetch
# reads it from snapshot.debian.org, which keeps every package Debian
# ever published. The digest below is the SHA256 that Debian's signed
# Packages index lists for this version, and the fetch fails with an
# error when the download disagrees.
#
# The lab ships in no release, so this firmware is not redistributed
# and needs no notice.
#
# Usage:
#   dev-cluster/firmware/fetch.sh   fetch the version pinned in VERSION
#
# Results land in firmware/dist/<version>/, cached in firmware/cache/:
# the plain build that every guest boots, its variable store, and the
# build with SMM that FIRMWARE=secureboot boots (dev-cluster/Makefile).

set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

for tool in curl sha256sum dpkg-deb python3; do
    command -v "$tool" >/dev/null || {
        echo "fetch.sh: missing required tool: $tool" >&2
        exit 1
    }
done

version="$(cat "$here/VERSION")"
digest="05c1593998ba58700ff86c062bde306321af1f5a2943ea9ceef6c2332f3f3374"

cache="$here/cache"
deb="$cache/ovmf-generic_${version}_all.deb"
out="$here/dist/$version"
mkdir -p "$cache"

if ! sha256sum --check --status <<<"$digest  $deb" >/dev/null 2>&1; then
    # snapshot.debian.org names each file by its SHA1, and answers the
    # SHA1 of a package version from its metadata service.
    encoded="${version//+/%2B}"
    sha1="$(curl -fsS --retry 3 "https://snapshot.debian.org/mr/binary/ovmf-generic/$encoded/binfiles" |
        python3 -c 'import json, sys; print(json.load(sys.stdin)["result"][0]["hash"])')"
    echo "downloading ovmf-generic $version"
    curl -fL --retry 5 --retry-all-errors --retry-delay 5 \
        --progress-bar -o "$deb" "https://snapshot.debian.org/file/$sha1"
    sha256sum --check --quiet <<<"$digest  $deb"
fi

rm -rf "$out"
mkdir -p "$out"
dpkg-deb --fsys-tarfile "$deb" |
    tar -x -m -C "$out" --strip-components=4 \
        ./usr/share/OVMF/OVMF_CODE_4M.fd ./usr/share/OVMF/OVMF_VARS_4M.fd \
        ./usr/share/OVMF/OVMF_CODE_4M.secboot.fd
echo "OVMF $version in $out"
