#!/usr/bin/env bash
#
# Vendor systemd-boot: the boot menu on liken's install media.
#
# Installed liken machines have no bootloader. Their firmware boots
# the kernel's own EFI stub straight from a boot entry that carries
# the whole command line. Install media cannot do that: when firmware
# boots a removable disk, it runs one well-known file
# (\EFI\BOOT\BOOTX64.EFI) with no arguments, and a kernel started with
# no arguments gets no initrd= parameter, so it loads no initramfs.
# Something on the stick must supply the command line, and on liken's
# stick that something is also the install menu: one entry per
# machine in the deployment. The operator picks the machine they are
# standing at, and that entry's options carry liken.machine=<name>.
#
# systemd-boot is the smallest program that does exactly this. It is
# a single ~130KB EFI application, not a bootloader in the GRUB sense:
# it has no modules, no scripting language, and no filesystem drivers
# of its own. It can be this small because the firmware already
# reads FAT and loads EFI binaries. systemd-boot only draws a
# menu from the plain-text entry files in /loader/entries on the disk
# it booted from, and chain-loads the chosen kernel's own EFI stub
# with the options that entry names. An entry can list several
# `initrd` lines, which systemd-boot concatenates in order; installed
# machines get the same composition from their three initrd=
# parameters.
#
# This script vendors the binary the same way as the kernel: prebuilt
# from Ubuntu's archive, pinned by version and digest. Ubuntu's pool
# carries only the current build of each series. When this version is
# superseded, the URL returns a 404 error, and the fix is the ordinary
# re-pin: systemd-boot/latest.sh --bump writes the new version into
# systemd-boot/VERSION and its URL and digest into
# systemd-boot/pin.toml.
#
# Usage:
#   systemd-boot/fetch.sh    fetch the version pinned in VERSION
#
# Results land in systemd-boot/dist/<version>/systemd-bootx64.efi,
# cached in systemd-boot/cache/.

set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source-path=SCRIPTDIR source=../../toolchains/pin.sh
source "$here/../../toolchains/pin.sh"

require_tools ar tar zstd sha256sum

version="$(cat "$here/VERSION")"

# The Ubuntu archive publishes per-file digests only inside the
# Packages indexes that its signed InRelease files list, not beside the
# files, so pin.toml holds the deb's digest. systemd-boot/latest.sh
# --bump verifies that chain and writes it.
deb="systemd-boot-efi_${version}_amd64.deb"

cache="$here/cache/$version"
out="$here/dist/$version"

fetch_pinned --resume systemd-boot-efi "$version" "$cache/$deb"

# A .deb file is an `ar` archive wrapping a compressed tarball of the
# files. liken extracts only the EFI binary from it.
staging="$(mktemp -d)"
trap 'rm -rf "$staging"' EXIT
ar p "$cache/$deb" data.tar.zst | tar --zstd -x -C "$staging" \
    ./usr/lib/systemd/boot/efi/systemd-bootx64.efi

rm -rf "$out"
mkdir -p "$out"
cp "$staging/usr/lib/systemd/boot/efi/systemd-bootx64.efi" "$out/systemd-bootx64.efi"

echo
echo "systemd-boot $version:"
stat -c '%s bytes' "$out/systemd-bootx64.efi"
