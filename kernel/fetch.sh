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
# Each mainline build publishes several packages. This script wants
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
#   kernel/fetch.sh 7.1.1      fetch a specific version instead
#
# To upgrade the kernel, change one line in kernel/VERSION. Every other
# upgrade in liken works the same way: edit a pin and commit it.
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
# against the sha256 checksums the archive publishes beside each
# build. This makes re-runs cheap and stops a torn download from
# passing as good. (The archive also signs its checksum file with the
# Ubuntu kernel team's GPG key. This script does not verify that
# signature. That gap is deliberate: the sha256 check proves the file
# arrived intact, but it does not prove who published it.)

set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# An ordinary Linux development machine has every tool this script
# needs: curl to fetch, ar (binutils) to open the .deb wrapper, tar and
# zstd to unpack the payload, sha256sum to verify it, and depmod (kmod)
# to index the modules at the end. This script checks for them first,
# so a missing tool stops it immediately instead of partway through a
# download.
for tool in curl ar tar zstd sha256sum depmod; do
    command -v "$tool" >/dev/null || {
        echo "fetch.sh: missing required tool: $tool" >&2
        exit 1
    }
done

arch="amd64"
version="${1:-$(cat "$here/VERSION")}"
base="https://kernel.ubuntu.com/mainline/v$version/$arch"

cache="$here/cache"
out="$here/dist/$version"
mkdir -p "$cache"

# The archive publishes one CHECKSUMS file per build: sha1 lines (40
# hex characters), followed by sha256 lines (64 characters), each in
# the form "<digest>  <filename>". This file also works as a package
# index. The filenames embed a build timestamp that has no other
# source, so this script finds the exact .deb names by searching this
# file instead of constructing URLs.
if ! checksums="$(curl -fsSL "$base/CHECKSUMS")"; then
    echo "fetch.sh: no mainline build found at $base" >&2
    echo "fetch.sh: (not every release builds successfully; check https://kernel.ubuntu.com/mainline/)" >&2
    exit 1
fi

# Files extract into a scratch directory first. This script touches
# dist/ only after everything arrives and verifies, so a failed run
# cannot leave a half-populated kernel behind.
staging="$(mktemp -d)"
trap 'rm -rf "$staging"' EXIT

# Download (with a cache) and unpack one package, identified by a
# filename pattern instead of an exact name. `ar p` streams the payload
# member to stdout. The payload may be a plain data.tar (current
# mainline builds), or compressed with zstd or xz (Ubuntu's own
# archives, older builds).
fetch() {
    local pattern="$1" line digest deb payload
    line="$(grep -E "^[0-9a-f]{64}  $pattern" <<<"$checksums" | head -n1)" || {
        echo "fetch.sh: no package matching '$pattern' in $base/CHECKSUMS" >&2
        exit 1
    }
    digest="${line%% *}"
    deb="${line##* }"

    # A cached file that still matches its checksum is good. This
    # script downloads anything else (missing, torn, or tampered)
    # again, and the new download must verify before the script uses
    # it.
    if ! sha256sum --check --status <<<"$digest  $cache/$deb" >/dev/null 2>&1; then
        echo "downloading $deb"
        curl -fL --progress-bar -o "$cache/$deb" "$base/$deb"
        sha256sum --check --quiet <<<"$digest  $cache/$deb"
    fi

    payload="$(ar t "$cache/$deb" | grep '^data\.tar')"
    case "$payload" in
        *.tar) ar p "$cache/$deb" "$payload" | tar -x -C "$staging" ;;
        *.zst) ar p "$cache/$deb" "$payload" | tar --zstd -x -C "$staging" ;;
        *.xz)  ar p "$cache/$deb" "$payload" | tar -xJ -C "$staging" ;;
        *)
            echo "fetch.sh: unexpected payload format in $deb: $payload" >&2
            exit 1
            ;;
    esac
}

fetch "linux-image-unsigned-.*-generic_.*_$arch\.deb"
fetch "linux-modules-.*-generic_.*_$arch\.deb"

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
