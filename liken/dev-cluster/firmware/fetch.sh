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
# The pin is a Debian package version (firmware/VERSION). The fetch
# reads the package from Debian's pool while sid holds it. The package
# leaves the pool when a newer build replaces it, so then the fetch
# reads it from snapshot.debian.org, which keeps every package Debian
# ever published. The pool comes first because the snapshot's servers
# can stay down for days, and a fetch that needs them fails the OS's
# build and its release, which run the UEFI smoke drill on this
# firmware. The digest below is the SHA256 of the package that
# latest.sh --bump downloaded and checked through sid's signed
# InRelease file and its Packages index, and the fetch fails with an
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
# shellcheck source-path=SCRIPTDIR source=../../../toolchains/pin.sh
source "$here/../../../toolchains/pin.sh"

require_tools sha256sum dpkg-deb python3

version="$(cat "$here/VERSION")"
digest="05c1593998ba58700ff86c062bde306321af1f5a2943ea9ceef6c2332f3f3374"

cache="$here/cache"
deb="$cache/ovmf-generic_${version}_all.deb"
out="$here/dist/$version"

encoded="${version//+/%2B}"
# ovmf-generic is a binary of the source package edk2, so the pool
# keeps it under pool/main/e/edk2/. fetch_verified fails the subshell
# alone when the pool has no such file, and the snapshot is the next
# source. Either source must give the bytes of the pinned SHA256.
if ! digest_matches "$digest" "$deb" &&
    ! (fetch_verified "https://deb.debian.org/debian/pool/main/e/edk2/ovmf-generic_${encoded}_all.deb" "$digest" "$deb"); then
    # snapshot.debian.org names each file by its SHA1, and answers the
    # SHA1 of a package version from its metadata service. The SHA1
    # only finds the file; the pinned SHA256 decides whether its bytes
    # are good.
    sha1="$(fetch_text "https://snapshot.debian.org/mr/binary/ovmf-generic/$encoded/binfiles" |
        python3 -c 'import json, sys; print(json.load(sys.stdin)["result"][0]["hash"])')"
    fetch_verified "https://snapshot.debian.org/file/$sha1" "$digest" "$deb"
fi

rm -rf "$out"
mkdir -p "$out"
dpkg-deb --fsys-tarfile "$deb" |
    tar -x -m -C "$out" --strip-components=4 \
        ./usr/share/OVMF/OVMF_CODE_4M.fd ./usr/share/OVMF/OVMF_VARS_4M.fd \
        ./usr/share/OVMF/OVMF_CODE_4M.secboot.fd
echo "OVMF $version in $out"
