#!/usr/bin/env bash
#
# Report the BlueZ and dbus releases that the bluetoothd image builds,
# and the newest release of each upstream.
#
# The Dockerfile beside this script fetches each release's source
# tarball and checks it against the sha256 in its build arguments:
# BLUEZ_VERSION and BLUEZ_SHA256, DBUS_VERSION and DBUS_SHA256. `liken`
# builds both from source, so the pins have no channel, and both
# channel columns print `-`. Under the rule in root plan 83 each pin
# follows the newest release of its project. The image that the build
# runs in is the `alpine` entry in toolchains/images.toml, and
# toolchains/latest.sh reports it.
#
# BlueZ publishes its tarballs on kernel.org, with one sha256sums.asc
# file for the directory that kernel.org's checksum autosigner
# clearsigns. The BlueZ bump copies a digest only from a sums file
# whose signature verifies against the autosigner's key:
# trust/keys/kernel-org-digest.sh makes the check, and
# trust/keys/README.md says where the key comes from. dbus publishes
# its tarballs on dbus.freedesktop.org, with a .sha256sum file beside
# each one. dbus
# numbers a development series with an odd minor version, such as
# 1.15, and a stable series with an even one, so the report reads the
# even series only. A bump reads each checksum from upstream's own
# file and writes it into the Dockerfile.
#
# The two projects release on separate schedules, so each one bumps
# on its own.
#
# Usage:
#   bluetoothd/latest.sh               report both pins
#   bluetoothd/latest.sh --bump        write the newest BlueZ release
#                                      and its checksum
#   bluetoothd/latest.sh --bump dbus   write the newest stable dbus
#                                      release and its checksum

set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
dockerfile="$here/Dockerfile"

bluez_archive="https://mirrors.edge.kernel.org/pub/linux/bluetooth"
dbus_archive="https://dbus.freedesktop.org/releases/dbus"

arg() { sed -n "s/^ARG $1=//p" "$dockerfile"; }
row() { printf '%s\t%s\t%s\t%s\t%s\t%s\t%s\n' "$@"; }

bluez="$(arg BLUEZ_VERSION)"
dbus="$(arg DBUS_VERSION)"

bluez_latest="$(curl -fsS --retry 3 --max-time 60 "$bluez_archive/" |
    grep -oE 'bluez-[0-9]+\.[0-9]+\.tar\.xz' |
    sed -e 's|^bluez-||' -e 's|\.tar\.xz$||' |
    sort -uV | tail -1)" || bluez_latest=""
dbus_latest="$(curl -fsS --retry 3 --max-time 60 "$dbus_archive/" |
    grep -oE 'dbus-[0-9]+\.[0-9]+\.[0-9]+\.tar\.xz"' |
    sed -e 's|^dbus-||' -e 's|\.tar\.xz"$||' |
    awk -F. '$2 % 2 == 0' | sort -uV | tail -1)" || dbus_latest=""

row bluez "$bluez" "${bluez_latest:-?}" - - - \
    "newest release on kernel.org; built static against musl in bluetoothd/Dockerfile"
row dbus "$dbus" "${dbus_latest:-?}" - - - \
    "newest stable release on dbus.freedesktop.org; bluetoothd links its libdbus-1.a"

[[ "${1:-}" == "--bump" ]] || exit 0

case "${2:-bluez}" in
bluez)
    [[ -n "$bluez_latest" ]] || {
        echo "latest.sh: kernel.org did not answer" >&2
        exit 1
    }
    [[ "$bluez_latest" != "$bluez" ]] || {
        echo "bluez $bluez is current"
        exit 0
    }
    tarball="bluez-$bluez_latest.tar.xz"
    sha256="$("$here/../../trust/keys/kernel-org-digest.sh" "$bluez_archive/sha256sums.asc" "$tarball")"
    sed -i -e "s|^ARG BLUEZ_VERSION=.*|ARG BLUEZ_VERSION=$bluez_latest|" \
        -e "s|^ARG BLUEZ_SHA256=.*|ARG BLUEZ_SHA256=$sha256|" "$dockerfile"
    echo "bluez: $bluez -> $bluez_latest"
    echo "the digest came from sha256sums.asc, signed by kernel.org's checksum autosigner"
    echo "next: docker buildx bake bluetoothd at the top of the repository; the build stops"
    echo "when a patch in patches/bluez/ no longer applies, and patches/README.md says what to do"
    ;;
dbus)
    [[ -n "$dbus_latest" ]] || {
        echo "latest.sh: dbus.freedesktop.org did not answer" >&2
        exit 1
    }
    [[ "$dbus_latest" != "$dbus" ]] || {
        echo "dbus $dbus is current"
        exit 0
    }
    tarball="dbus-$dbus_latest.tar.xz"
    sha256="$(curl -fsSL --retry 3 --max-time 60 "$dbus_archive/$tarball.sha256sum" |
        awk -v t="$tarball" '$2 == t || $2 == "*" t { print $1 }')"
    [[ "$sha256" =~ ^[0-9a-f]{64}$ ]] || {
        echo "latest.sh: $dbus_archive/$tarball.sha256sum names no checksum for $tarball" >&2
        exit 1
    }
    sed -i -e "s|^ARG DBUS_VERSION=.*|ARG DBUS_VERSION=$dbus_latest|" \
        -e "s|^ARG DBUS_SHA256=.*|ARG DBUS_SHA256=$sha256|" "$dockerfile"
    echo "dbus: $dbus -> $dbus_latest"
    echo "dbus.freedesktop.org's sha256sum file stands behind these bytes"
    echo "next: docker buildx bake bluetoothd at the top of the repository"
    ;;
*)
    echo "latest.sh: no pin named $2; try bluez or dbus" >&2
    exit 1
    ;;
esac
