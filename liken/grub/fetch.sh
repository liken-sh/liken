#!/usr/bin/env bash
#
# Vendor GRUB: the bootloader on liken's BIOS machines.
#
# UEFI machines need no bootloader at all. Their firmware holds boot
# entries and loads the kernel's own EFI stub. BIOS firmware offers
# nothing like that: it loads one sector, the MBR's 440 bytes of boot
# code, and jumps into it. Those bytes cannot hold a program, so GRUB
# splits itself in two: the MBR stage holds only the disk address of
# the *core image*, a compressed program (in liken's layout, sitting
# in the raw biosBoot partition) that carries real filesystem drivers
# and a script interpreter. liken ships both halves as release
# artifacts. The installer and init write them to disk the same way
# they write partition tables: directly, with no grub-install.
#
# Two debs are vendored, pinned by version and digest like
# systemd-boot's:
#
#   grub-pc-bin   the BIOS ("i386-pc") build: boot.img (the MBR
#                 stage) and the module tree grub-mkimage links core
#                 images from
#   grub2-common  grub-mkimage itself, the tool that does the linking.
#
# The fetch includes the tool instead of taking it from the build
# host, so the pair can never drift apart: a grub-mkimage from one GRUB version
# linking modules from another is exactly the kind of mismatch pinning
# exists to prevent. The binary is dynamically linked, which sets an
# opposite requirement: the libraries that the pinned grub-mkimage
# links must be present on every host that builds the OS, which is
# Ubuntu 26.04 in CI and on a workstation. grub-mkimage links
# libdevmapper, so a pin from a newer series than the build hosts can
# need a libdevmapper that they do not have. Run the new
# grub-mkimage --version on the build host before a re-pin.
#
# When Ubuntu supersedes this version, the URLs return a 404 error,
# and the fix is the ordinary re-pin: grub/latest.sh --bump writes
# the new version into grub/VERSION and the URLs and digests of both
# debs into grub/pin.toml.
#
# Usage:
#   grub/fetch.sh    fetch the version pinned in VERSION
#
# Results land in grub/dist/<version>/: the i386-pc module tree,
# grub-mkimage, and grub-boot.img (boot.img under its artifact name).
# Debs are cached in grub/cache/.

set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source-path=SCRIPTDIR source=../../toolchains/pin.sh
source "$here/../../toolchains/pin.sh"

require_tools ar tar zstd sha256sum

version="$(cat "$here/VERSION")"

# The Ubuntu archive publishes per-file digests only inside the
# Packages indexes that its signed InRelease files list, not beside the
# files, so pin.toml holds the digests of both debs. grub/latest.sh
# --bump verifies that chain and writes them.
cache="$here/cache/$version"
out="$here/dist/$version"

pc_bin="grub-pc-bin_${version}_amd64.deb"
common="grub2-common_${version}_amd64.deb"
fetch_pinned --resume grub-pc-bin "$version" "$cache/$pc_bin"
fetch_pinned --resume grub2-common "$version" "$cache/$common"

# A .deb file is an `ar` archive wrapping a compressed tarball of the
# files. This script takes the whole i386-pc tree, not just the
# modules the core image links today: moddep.lst in that tree is how
# grub-mkimage resolves module dependencies, and future module
# choices should not require a re-vendor.
staging="$(mktemp -d)"
trap 'rm -rf "$staging"' EXIT
ar p "$cache/$pc_bin" data.tar.zst | tar --zstd -x -C "$staging" \
    ./usr/lib/grub/i386-pc
ar p "$cache/$common" data.tar.zst | tar --zstd -x -C "$staging" \
    ./usr/bin/grub-mkimage

rm -rf "$out"
mkdir -p "$out"
cp -r "$staging/usr/lib/grub/i386-pc" "$out/i386-pc"
cp "$staging/usr/bin/grub-mkimage" "$out/grub-mkimage"

# boot.img ships under its artifact name. Only its first 440 bytes
# ever reach a disk (the rest of the sector belongs to the partition
# table). Two fields inside are patched per machine with the core
# image's location; init does that arithmetic at install time.
cp "$out/i386-pc/boot.img" "$out/grub-boot.img"

echo
echo "grub $version:"
stat -c '%s bytes  grub-boot.img' "$out/grub-boot.img"
"$out/grub-mkimage" --version
