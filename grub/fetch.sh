#!/usr/bin/env bash
#
# Vendor GRUB: the bootloader on liken's BIOS machines.
#
# UEFI machines need no bootloader at all — their firmware holds boot
# entries and loads the kernel's own EFI stub. BIOS firmware offers
# nothing like that: it loads one sector, the MBR's 440 bytes of boot
# code, and jumps in. Those bytes can't hold a program, so GRUB
# splits itself: the MBR stage knows only the disk address of the
# *core image*, a compressed program (in liken's layout, sitting in
# the raw biosBoot partition) that carries real filesystem drivers
# and a script interpreter. liken ships both halves as release
# artifacts, and the installer and init write them to disk the same
# way they write partition tables: directly, no grub-install.
#
# Two debs are vendored, pinned by version and digest like
# systemd-boot's:
#
#   grub-pc-bin   the BIOS ("i386-pc") build: boot.img (the MBR
#                 stage) and the module tree grub-mkimage links core
#                 images from
#   grub2-common  grub-mkimage itself, the tool that does the linking
#
# The tool rides along rather than coming from the build host so the
# pair can never skew: a grub-mkimage from one GRUB version linking
# modules from another is exactly the kind of quiet mismatch pinning
# exists to prevent. (The binary is dynamically linked, but only
# against libraries every Ubuntu host has; the Makefile's build of
# core.img proves it runs.)
#
# When Ubuntu supersedes this version the URLs 404, and the fix is
# the ordinary one-line re-pin: new version in grub/VERSION, new
# digests below (sha256sum the two debs).
#
# Usage:
#   grub/fetch.sh    fetch the version pinned in VERSION
#
# Results land in grub/dist/<version>/: the i386-pc module tree,
# grub-mkimage, and grub-boot.img (boot.img under its artifact name).
# Debs are cached in grub/cache/.

set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

for tool in curl ar tar zstd sha256sum; do
    command -v "$tool" >/dev/null || {
        echo "fetch.sh: missing required tool: $tool" >&2
        exit 1
    }
done

version="${1:-$(cat "$here/VERSION")}"

# Per-deb digests, pinned beside the version they belong to. The
# Ubuntu archive publishes per-file digests only inside its signed
# Release indexes, not beside the files, so the pins live here where
# a version bump has to update them deliberately.
declare -A pc_bin_sha256=(
    ["2.14-2ubuntu3"]="187f86d4c802a250d417f9f0d2a4095c2e3995ea5194f55a6d9ec27fd16f5055"
)
declare -A common_sha256=(
    ["2.14-2ubuntu3"]="5d45c86963028b5d027d65c9126580742f8924195f71265144dc7ea6422acdbd"
)

pool="http://archive.ubuntu.com/ubuntu/pool/main/g/grub2"
cache="$here/cache/$version"
out="$here/dist/$version"
mkdir -p "$cache"

# fetch <deb filename> <digest>: download into the cache unless the
# cached copy already verifies.
fetch() {
    local deb="$1" digest="$2"
    if [[ -z "$digest" ]]; then
        echo "fetch.sh: no pinned digest for $deb; add it above" >&2
        exit 1
    fi
    if ! sha256sum --check --status <<<"$digest  $cache/$deb" >/dev/null 2>&1; then
        echo "downloading $deb"
        curl -fL --progress-bar -o "$cache/$deb" "$pool/$deb"
        sha256sum --check --quiet <<<"$digest  $cache/$deb"
    fi
}

pc_bin="grub-pc-bin_${version}_amd64.deb"
common="grub2-common_${version}_amd64.deb"
fetch "$pc_bin" "${pc_bin_sha256[$version]:-}"
fetch "$common" "${common_sha256[$version]:-}"

# A .deb is an `ar` archive wrapping a compressed tarball of the
# files. The whole i386-pc tree comes along, not just the modules the
# core image links today: moddep.lst in that tree is how grub-mkimage
# resolves module dependencies, and future module choices shouldn't
# require a re-vendor.
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
# table), and two fields inside get patched per machine with the core
# image's location; init does that arithmetic at install time.
cp "$out/i386-pc/boot.img" "$out/grub-boot.img"

echo
echo "grub $version:"
stat -c '%s bytes  grub-boot.img' "$out/grub-boot.img"
"$out/grub-mkimage" --version
