#!/usr/bin/env bash
#
# Vendor a pre-built Linux kernel from Ubuntu's mainline builds.
#
# liken deliberately does not compile kernels. Building Linux once is a
# fine rite of passage, but *maintaining* kernel builds — configs, CVEs,
# toolchains — is the grind that has worn down more than one small distro,
# and it teaches nothing about what liken is here to teach. Instead we
# ride along with Canonical's kernel team: their mainline archive
# (https://kernel.ubuntu.com/mainline/) publishes every upstream release —
# stable, point releases, even RCs — as pre-built Debian packages, usually
# within a day of the tag. Crucially, these are vanilla kernels: built
# from Linus's tree with no Ubuntu patches, using Ubuntu's "generic"
# configuration. What we boot is what upstream shipped.
#
# A Debian package needs no Debian tooling to open. A .deb is an `ar`
# archive — the 1970s static-library format — wrapping a tarball
# (data.tar.zst) of the package's files. So `ar`, `tar`, and `zstd` are
# the entire extraction toolchain, and this script works the same on any
# Linux machine.
#
# Each mainline build publishes several packages; we want exactly two:
#
#   linux-image-unsigned-*   the kernel itself: one self-decompressing
#                            file, /boot/vmlinuz-*
#   linux-modules-*          everything built as a loadable module, plus
#                            the build's config and System.map
#
# ("unsigned" because the signed variant only matters when chaining trust
# from Canonical's Secure Boot certificates. When liken gets to Secure
# Boot, the plan is to sign with our own keys anyway.)
#
# Usage:
#
#   kernel/fetch.sh            fetch the version pinned in kernel/VERSION
#   kernel/fetch.sh 7.1.1      fetch a specific version instead
#
# Upgrading the kernel is a one-line change to kernel/VERSION — the same
# shape as every other upgrade in liken: edit a pin, commit.
#
# Results land in kernel/dist/<version>/:
#
#   vmlinuz                  the bootable kernel image
#   config                   the exact build configuration, for answering
#                            "is that driver built in, a module, or absent?"
#   release                  the kernel's release string, e.g.
#                            "7.1.2-070102-generic" — later build steps
#                            need it to find the module directory
#   lib/modules/<release>/   the module tree, indexed and laid out exactly
#                            as it will appear in the initramfs
#
# Downloads are cached in kernel/cache/ and verified against the sha256
# checksums the archive publishes alongside each build, so re-runs are
# cheap and a torn download can't slip through. (The archive also signs
# its checksum file with the Ubuntu kernel team's GPG key; we don't
# verify that signature, a conscious gap — sha256 gives us integrity,
# not provenance.)

set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# Everything we need is stock on an ordinary Linux development machine:
# curl to fetch, ar (binutils) to open the .deb wrapper, tar + zstd to
# unpack the payload, sha256sum to verify it, and depmod (kmod) to index
# the modules at the end. Fail up front, not mid-download.
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

# The archive publishes a CHECKSUMS file per build: sha1 lines (40 hex
# chars) followed by sha256 lines (64), each "<digest>  <filename>". It
# doubles as our package index — the filenames embed a build timestamp we
# can't otherwise guess, so we discover the exact .deb names by grepping
# it rather than constructing URLs.
if ! checksums="$(curl -fsSL "$base/CHECKSUMS")"; then
    echo "fetch.sh: no mainline build found at $base" >&2
    echo "fetch.sh: (not every release builds successfully; check https://kernel.ubuntu.com/mainline/)" >&2
    exit 1
fi

# Files extract into a scratch directory first; dist/ only gets touched
# once everything has arrived and verified, so a failed run can't leave a
# half-populated kernel behind.
staging="$(mktemp -d)"
trap 'rm -rf "$staging"' EXIT

# Download (with cache) and unpack one package, identified by a filename
# pattern rather than an exact name. `ar p` streams the payload member to
# stdout; the payload may be a plain data.tar (current mainline builds) or
# compressed with zstd or xz (Ubuntu's own archives, older builds).
fetch() {
    local pattern="$1" line digest deb payload
    line="$(grep -E "^[0-9a-f]{64}  $pattern" <<<"$checksums" | head -n1)" || {
        echo "fetch.sh: no package matching '$pattern' in $base/CHECKSUMS" >&2
        exit 1
    }
    digest="${line%% *}"
    deb="${line##* }"

    # A cached file that still matches its checksum is good; anything
    # else — missing, torn, or tampered — gets re-downloaded and must
    # verify before we touch it.
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

# The kernel names its own world: the "release string" (uname -r) is
# embedded in every artifact's filename and directory layout, and it's
# how the kernel locates its modules at runtime (/lib/modules/$(uname -r)).
# Recover it from the image filename rather than guessing.
vmlinuz=("$staging"/boot/vmlinuz-*)
release="$(basename "${vmlinuz[0]}")"
release="${release#vmlinuz-}"

# Modules historically live in /lib/modules; distributions that have
# completed the "usr merge" ship them in /usr/lib/modules instead. Take
# whichever this package used.
modules="$staging/lib/modules"
[[ -d "$modules" ]] || modules="$staging/usr/lib/modules"

rm -rf "$out"
mkdir -p "$out/lib"
cp "${vmlinuz[0]}" "$out/vmlinuz"
cp "$staging"/boot/config-* "$out/config"
echo "$release" >"$out/release"
mv "$modules" "$out/lib/modules"

# modprobe never scans the module tree at runtime — it reads an index,
# modules.dep(.bin), that maps each module to the modules it depends on.
# depmod builds that index. Installing the .deb normally would have run
# this in a post-install hook; since we unpack by hand, we run it by
# hand — at build time, here on the host, so the booted system never
# needs depmod at all.
depmod --basedir "$out" "$release"

echo
echo "kernel $version ($release):"
du -sh "$out/vmlinuz" "$out/lib/modules"
