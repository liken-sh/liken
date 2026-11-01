#!/usr/bin/env bash
#
# Vendor a pre-built Linux kernel from Ubuntu's mainline builds.
#
# liken does not compile kernels. Building Linux once is worth doing.
# But maintaining kernel builds (configs, CVEs, toolchains) is ongoing
# work. This work has exhausted many small distros, and it teaches
# nothing about what liken is here to teach.
#
# Instead, liken relies on Canonical's kernel team. Their mainline
# archive (https://kernel.ubuntu.com/mainline/) publishes every
# upstream release (stable, point releases, and even RCs) as pre-built
# Debian packages, usually within a day of the tag. These are vanilla
# kernels: the archive builds them from Linus's tree with no Ubuntu
# patches, using Ubuntu's "generic" configuration. The kernel liken
# boots is the kernel upstream shipped.
#
# A Debian package needs no Debian tooling to open. A .deb file is an
# `ar` archive, the 1970s static-library format. It wraps a tarball
# (data.tar.zst) of the package's files. So `ar`, `tar`, and `zstd` are
# the entire extraction toolchain, and this script works the same way
# on any Linux machine.
#
# Each mainline build publishes several packages. This script extracts
# exactly two of them:
#
#   linux-image-unsigned-*   the kernel itself: one self-decompressing
#                            file, /boot/vmlinuz-*
#   linux-modules-*          everything built as a loadable module, plus
#                            the build's config and System.map
#
# ("unsigned" because the signed variant matters only when it chains
# trust from Canonical's Secure Boot certificates. When liken adds
# Secure Boot support, the plan is to sign with liken's own keys.)
#
# Usage:
#
#   kernel/fetch.sh            fetch the version pinned in kernel/VERSION
#
# To upgrade the kernel, run kernel/latest.sh --bump. It writes the new
# version into kernel/VERSION and the new packages' URLs and digests
# into kernel/pin.toml, and the commit carries both.
#
# Results land in kernel/dist/<version>/:
#
#   vmlinuz                  the bootable kernel image
#   config                   the exact build configuration; use it to
#                            answer "is that driver built in, a module,
#                            or absent?"
#   release                  the kernel's release string, e.g.
#                            "7.1.2-070102-generic". Later build steps
#                            need it to find the module directory.
#   lib/modules/<release>/   the module tree, indexed and laid out
#                            exactly as it will appear in the initramfs
#
# This script caches downloads in kernel/cache/ and verifies them
# against the sha256 digests in kernel/pin.toml. This makes re-runs
# cheap and stops a torn download from passing as good. The digests
# come from the CHECKSUMS file that the archive publishes beside each
# build, copied into pin.toml when the pin moved, so a package that
# changes upstream after that fails the check. kernel/latest.sh
# --bump copies the digests only after the checksum file's signature
# verifies against the Ubuntu kernel team's key in kernel-ppa.asc, so
# the pinned digests name bytes that the kernel team published.

set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source-path=SCRIPTDIR source=../../toolchains/pin.sh
source "$here/../../toolchains/pin.sh"

# An ordinary Linux development machine has every tool this script
# needs: curl to fetch, ar (binutils) to open the .deb wrapper, tar and
# zstd to unpack the payload, sha256sum to verify it, and depmod (kmod)
# to index the modules at the end. This script checks for them first,
# so a missing tool stops it immediately instead of partway through a
# download.
require_tools ar tar zstd sha256sum depmod

version="$(cat "$here/VERSION")"

cache="$here/cache"
out="$here/dist/$version"

# Files extract into a scratch directory first. This script touches
# dist/ only after everything arrives and verifies, so a failed run
# cannot leave a half-populated kernel behind.
staging="$(mktemp -d)"
trap 'rm -rf "$staging"' EXIT

# Download (with a cache) and unpack one package, named by its entry
# in pin.toml. The file names embed a build timestamp that has no
# other source than the build's CHECKSUMS file, so the cache takes the
# name from the pinned url instead of building it from the version.
# `ar p` streams the payload member to stdout. The payload may be a
# plain data.tar (current mainline builds), or compressed with zstd or
# xz (Ubuntu's own archives, older builds).
fetch() {
    local entry="$1" url deb payload
    url="$(pin_value "$entry" url)"
    deb="${url##*/}"

    # These .debs are large, and a long stream sometimes breaks partway
    # through, so each retry resumes from the bytes already on disk.
    fetch_pinned --resume "$entry" "$version" "$cache/$deb"

    payload="$(ar t "$cache/$deb" | grep '^data\.tar')"
    case "$payload" in
        *.tar) ar p "$cache/$deb" "$payload" | tar -x -C "$staging" ;;
        *.zst) ar p "$cache/$deb" "$payload" | tar --zstd -x -C "$staging" ;;
        *.xz)  ar p "$cache/$deb" "$payload" | tar -xJ -C "$staging" ;;
        *) fail "unexpected payload format in $deb: $payload" ;;
    esac
}

fetch linux-image-unsigned
fetch linux-modules

# The kernel's "release string" (uname -r) is embedded in every
# artifact's filename and directory layout. The kernel uses this
# string at runtime to locate its modules (/lib/modules/$(uname -r)).
# This script reads the string from the image filename instead of
# guessing it.
vmlinuz=("$staging"/boot/vmlinuz-*)
release="$(basename "${vmlinuz[0]}")"
release="${release#vmlinuz-}"

# Modules traditionally live in /lib/modules. Distributions that have
# completed the "usr merge" ship them in /usr/lib/modules instead. This
# script uses whichever path this package used.
modules="$staging/lib/modules"
[[ -d "$modules" ]] || modules="$staging/usr/lib/modules"

rm -rf "$out"
mkdir -p "$out/lib"
cp "${vmlinuz[0]}" "$out/vmlinuz"
cp "$staging"/boot/config-* "$out/config"
echo "$release" >"$out/release"
mv "$modules" "$out/lib/modules"

# modprobe never scans the module tree at runtime. It reads an index,
# modules.dep(.bin), that maps each module to the modules it depends
# on. depmod builds that index. A normal .deb install runs this step in
# a post-install hook. This script unpacks the package by hand, so it
# runs depmod by hand too, at build time, here on the host. As a
# result, the booted system never needs depmod at all.
depmod --basedir "$out" "$release"

echo
echo "kernel $version ($release):"
du -sh "$out/vmlinuz" "$out/lib/modules"
