#!/usr/bin/env bash
#
# Vendor GRUB: the bootloader on liken's BIOS machines.
#
# UEFI machines need no bootloader at all. Their firmware holds boot
# entries and loads the kernel's own EFI stub. BIOS firmware offers
# nothing like that: it loads one sector, the MBR's 440 bytes of boot
# code, and jumps into it. Those bytes cannot hold a program, so GRUB
# splits itself in two: the MBR stage knows only the disk address of
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
#   grub-common   grub-mkimage itself, the tool that does the linking
#                 (the 2.14 packaging moved it to grub2-common; a
#                 re-pin past 2.12 must move this fetch with it)
#
# The fetch includes the tool instead of taking it from the build
# host, so the pair can never drift apart: a grub-mkimage from one GRUB version
# linking modules from another is exactly the kind of mismatch pinning
# exists to prevent. The binary is dynamically linked, which sets an
# opposite requirement: the pinned series must be old enough that its
# library demands are met everywhere builds run. The 2.14 series
# showed this problem (its grub-mkimage wants a libdevmapper newer
# than CI has), which is why the pin sits on 2.12, the current LTS
# series. The pin should follow the oldest platform liken builds on.
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

for tool in curl ar tar zstd sha256sum; do
    command -v "$tool" >/dev/null || {
        echo "fetch.sh: missing required tool: $tool" >&2
        exit 1
    }
done

version="${1:-$(cat "$here/VERSION")}"

# Per-deb digests, pinned beside the version they belong to. The
# Ubuntu archive publishes per-file digests only inside its signed
# Release indexes, not beside the files, so this file holds the pins.
# A version bump must update them deliberately.
declare -A pc_bin_sha256=(
    ["2.12-1ubuntu7.3"]="698d80bc51c0c593fbc829758365a0e4bce96819e2c8c754ae77dfe39b0388df"
)
declare -A common_sha256=(
    ["2.12-1ubuntu7.3"]="464e7c3a9f261b2120a6755fbf753384669c666a5cc7cd571cf836d6da88373f"
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
common="grub-common_${version}_amd64.deb"
fetch "$pc_bin" "${pc_bin_sha256[$version]:-}"
fetch "$common" "${common_sha256[$version]:-}"

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
