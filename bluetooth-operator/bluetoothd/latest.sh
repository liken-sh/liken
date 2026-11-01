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
# trust/keys/README.md says where the key comes from.
#
# dbus publishes its tarballs on dbus.freedesktop.org, each with a
# detached signature beside it, and signs no checksum file. So the dbus
# bump downloads the tarball and its .asc, verifies the signature
# against dbus.asc beside this script, and writes the digest of the
# verified bytes. dbus.asc is the key of Simon McVittie, who signs the
# dbus releases, exported alone from debian/upstream/signing-key.asc
# of Debian's dbus package, and keys.openpgp.org serves the same
# primary key. The bump accepts a signature from that key only, so a
# release that another maintainer signs stops the bump until a person
# checks the new key and adds it. dbus numbers a development series
# with an odd minor version, such as 1.15, and a stable series with an
# even one, so the report reads the even series only.
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
# shellcheck source-path=SCRIPTDIR source=../../toolchains/pin.sh
source "$here/../../toolchains/pin.sh"
dockerfile="$here/Dockerfile"

bluez_archive="https://mirrors.edge.kernel.org/pub/linux/bluetooth"
dbus_archive="https://dbus.freedesktop.org/releases/dbus"
# The fingerprint of the primary key in dbus.asc.
dbus_signer="DA98F25C0871C49A59EAFF2C4DE8FF2A63C7CC90"

arg() { sed -n "s/^ARG $1=//p" "$dockerfile"; }

bluez="$(arg BLUEZ_VERSION)"
dbus="$(arg DBUS_VERSION)"

bluez_latest="$(fetch_text "$bluez_archive/" |
    grep -oE 'bluez-[0-9]+\.[0-9]+\.tar\.xz' |
    sed -e 's|^bluez-||' -e 's|\.tar\.xz$||' |
    sort -uV | tail -1)" || bluez_latest=""
dbus_latest="$(fetch_text "$dbus_archive/" |
    grep -oE 'dbus-[0-9]+\.[0-9]+\.[0-9]+\.tar\.xz"' |
    sed -e 's|^dbus-||' -e 's|\.tar\.xz"$||' |
    awk -F. '$2 % 2 == 0' | sort -uV | tail -1)" || dbus_latest=""

row bluez "$bluez" "$bluez_latest" - - - \
    "newest release on kernel.org; built static against musl in bluetoothd/Dockerfile"
row dbus "$dbus" "$dbus_latest" - - - \
    "newest stable release on dbus.freedesktop.org; bluetoothd links its libdbus-1.a"

[[ "${1:-}" == "--bump" ]] || exit 0

case "${2:-bluez}" in
bluez)
    bump_needed bluez "$bluez" "$bluez_latest" || exit 0
    tarball="bluez-$bluez_latest.tar.xz"
    sha256="$("$here/../../trust/keys/kernel-org-digest.sh" "$bluez_archive/sha256sums.asc" "$tarball")"
    set_line "$dockerfile" '^ARG BLUEZ_VERSION=' "ARG BLUEZ_VERSION=$bluez_latest"
    set_line "$dockerfile" '^ARG BLUEZ_SHA256=' "ARG BLUEZ_SHA256=$sha256"
    bumped bluez "$bluez" "$bluez_latest"
    echo "the digest came from sha256sums.asc, signed by kernel.org's checksum autosigner"
    next_step "docker buildx bake bluetoothd at the top of the repository; the build stops"
    echo "when a patch in patches/bluez/ no longer applies, and patches/README.md says what to do"
    ;;
dbus)
    bump_needed dbus "$dbus" "$dbus_latest" || exit 0
    tarball="dbus-$dbus_latest.tar.xz"
    work="$(mktemp -d)"
    trap 'rm -rf "$work"' EXIT
    fetch_file "$dbus_archive/$tarball" "$work/$tarball"
    fetch_file "$dbus_archive/$tarball.asc" "$work/$tarball.asc"
    verify_signature --signer "$dbus_signer" "$here/dbus.asc" "$work/$tarball.asc" "$work/$tarball"
    sha256="$(sha256sum "$work/$tarball" | cut -d' ' -f1)"
    set_line "$dockerfile" '^ARG DBUS_VERSION=' "ARG DBUS_VERSION=$dbus_latest"
    set_line "$dockerfile" '^ARG DBUS_SHA256=' "ARG DBUS_SHA256=$sha256"
    bumped dbus "$dbus" "$dbus_latest"
    echo "the digest came from the tarball, whose signature verified against dbus.asc"
    next_step "docker buildx bake bluetoothd at the top of the repository"
    ;;
*) fail "no pin named $2; try bluez or dbus" ;;
esac
