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
#   grub-common   grub-mkimage itself, the tool that does the linking.
#                 Ubuntu's 2.14 packaging moved it to grub2-common, so
#                 mkimage_package below names the deb for each version.
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
# and the fix is the ordinary one-line re-pin: new version in
# grub/VERSION, new digests below (sha256sum the two debs).
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

version="${1:-$(cat "$here/VERSION")}"

# Per-deb digests, pinned beside the version they belong to. The
# Ubuntu archive publishes per-file digests only inside its signed
# Release indexes, not beside the files, so this file holds the pins.
# grub/latest.sh --bump adds the entries of each new version, and the
# entries of older versions stay.
declare -A pc_bin_sha256=(
    ["2.14-2ubuntu3"]="187f86d4c802a250d417f9f0d2a4095c2e3995ea5194f55a6d9ec27fd16f5055"
    ["2.12-1ubuntu7.3"]="698d80bc51c0c593fbc829758365a0e4bce96819e2c8c754ae77dfe39b0388df"
)
# The deb that holds grub-mkimage, for each version. A version that
# is not named here takes grub2-common, the package of the 2.14
# packaging and later.
declare -A mkimage_package=(
    ["2.12-1ubuntu7.3"]="grub-common"
)
declare -A common_sha256=(
    ["2.14-2ubuntu3"]="5d45c86963028b5d027d65c9126580742f8924195f71265144dc7ea6422acdbd"
    ["2.12-1ubuntu7.3"]="464e7c3a9f261b2120a6755fbf753384669c666a5cc7cd571cf836d6da88373f"
)

pool="http://archive.ubuntu.com/ubuntu/pool/main/g/grub2"
cache="$here/cache/$version"
out="$here/dist/$version"

pc_bin="grub-pc-bin_${version}_amd64.deb"
common="${mkimage_package[$version]:-grub2-common}_${version}_amd64.deb"
fetch_verified --resume "$pool/$pc_bin" "${pc_bin_sha256[$version]:-}" "$cache/$pc_bin"
fetch_verified --resume "$pool/$common" "${common_sha256[$version]:-}" "$cache/$common"

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
