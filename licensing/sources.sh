#!/usr/bin/env bash
#
# Mirror the corresponding source for everything liken redistributes.
#
# liken's own code is MIT, but a release also carries other people's
# work: the kernel, the netfilter tools, mke2fs, the iSCSI and NFS
# clients, systemd-boot, and GRUB are all GPL- or LGPL-licensed, and
# those licenses attach one real obligation to redistributing their
# binaries: whoever gets the binary must be able to get the source
# that built it. The GPL has a clause written for exactly a release
# channel's shape: distributing object code "from a designated place"
# is compliant when the source is offered from the same place. So the
# channel that serves the binaries also serves the sources, under
# sources/<component>/<version>/, and this script assembles that tree.
#
# We mirror rather than link. Upstream URLs rot — Ubuntu's pool 404s
# a package the day it is superseded, which the grub and systemd-boot
# fetch scripts already document — and a source offer that can rot is
# not an offer. Mirrors are keyed by component version, not release
# version, because sources only change when a pin bumps: every
# release built from the same pins shares the same mirrored sources.
#
# Every mirrored file is pinned here by sha256, the same discipline
# as every fetch script in this repo. Some pins track another
# domain's pin and must move together with it; each pin below names
# what it follows. A domain pin that bumps without its matching
# update here fails the digest or URL check, so a release cannot
# quietly ship binaries whose sources aren't mirrored.
#
# Two components need no mirror. k3s is Apache-2.0, which asks for
# notices, not source; its release page carries the source anyway,
# and the GPL-licensed userland embedded inside the k3s binary is
# built by the same k3s-root recipe mirrored under xtables/. The CA
# bundle is its own source: the PEM file is the preferred form for
# modifying it, so the mirror is a copy of the artifact itself.
#
# Usage:
#   licensing/sources.sh    mirror the sources for the pinned versions
#
# Results land in licensing/dist/sources/<component>/<version>/, laid
# out exactly as the channel serves them at
# https://releases.liken.sh/sources/. Downloads are cached in
# licensing/cache/.

set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

for tool in curl sha256sum; do
    command -v "$tool" >/dev/null || {
        echo "sources.sh: missing required tool: $tool" >&2
        exit 1
    }
done

# The component versions come from the domains that pin them, so this
# script can never disagree with what a release actually carries.
kernel_version="$(cat "$here/../kernel/VERSION")"
xtables_version="$(cat "$here/../xtables/VERSION")"
trust_version="$(cat "$here/../trust/VERSION")"
e2fsprogs_version="$(cat "$here/../e2fsprogs/VERSION")"
openiscsi_version="$(cat "$here/../open-iscsi/VERSION")"
nfsutils_version="$(cat "$here/../nfs-utils/VERSION")"
systemdboot_version="$(cat "$here/../systemd-boot/VERSION")"
grub_version="$(cat "$here/../grub/VERSION")"

cache="$here/cache"
out="$here/dist/sources"

# mirror <component>/<version> <filename> <sha256> <url>: download
# once into the cache, verify against the pin every time, and place
# the file in the tree the channel serves. Hardlinks keep the big
# tarballs from existing twice on disk. The retries matter here more
# than in most fetch scripts: this one pulls from several upstreams
# in a row (kernel.org, GitHub, GNU, Launchpad), and a single
# transient 502 from any of them would otherwise fail a release.
mirror() {
    local dir="$1" file="$2" sha="$3" url="$4"
    mkdir -p "$cache/$dir" "$out/$dir"
    if [[ ! -f "$cache/$dir/$file" ]]; then
        echo "mirroring $dir/$file"
        curl -fsSL --retry 5 --retry-delay 5 "$url" -o "$cache/$dir/$file.partial"
        mv "$cache/$dir/$file.partial" "$cache/$dir/$file"
    fi
    echo "$sha  $cache/$dir/$file" | sha256sum --check --quiet || {
        echo "sources.sh: $dir/$file does not match its pin; if a domain's" >&2
        echo "version bumped, update the matching source pin in this script" >&2
        exit 1
    }
    ln -f "$cache/$dir/$file" "$out/$dir/$file" 2>/dev/null \
        || cp "$cache/$dir/$file" "$out/$dir/$file"
}

# place <component>/<version> <filename> <path>: a file another
# domain already fetched and verified against its own pin; it only
# needs to be laid out in the sources tree.
place() {
    local dir="$1" file="$2" src="$3"
    [[ -f "$src" ]] || {
        echo "sources.sh: $src is missing; build its domain first" >&2
        exit 1
    }
    mkdir -p "$out/$dir"
    ln -f "$src" "$out/$dir/$file" 2>/dev/null || cp "$src" "$out/$dir/$file"
}

# The kernel: the upstream tarball Canonical's mainline archive
# builds from (their builds are vanilla, so Linus's tarball is the
# whole source), plus the exact build configuration, which the GPL
# counts as part of the corresponding source and which the kernel
# domain already extracts from the module package.
# Tracks kernel/VERSION.
mirror "kernel/$kernel_version" "linux-$kernel_version.tar.xz" \
    "37198c93727be247c9fb5309bb86cd5e496c61e5322cd8c4eca9476bb0b5883f" \
    "https://cdn.kernel.org/pub/linux/kernel/v${kernel_version%%.*}.x/linux-$kernel_version.tar.xz"
place "kernel/$kernel_version" "config" "$here/../kernel/dist/$kernel_version/config"

# The xtables binaries are iptables, built by k3s-root's buildroot
# recipe: the program's own source, the recipe, and buildroot itself,
# which carries the build scripts (and pins every remaining package
# it builds by hash). The same three files are the source for the
# GPL-licensed userland embedded in the k3s binary, which k3s
# assembles from the same k3s-root version.
# iptables tracks what k3s-root builds (xtables-legacy-multi
# --version prints it); buildroot tracks k3s-root's scripts/download.
mirror "xtables/$xtables_version" "iptables-1.8.11.tar.xz" \
    "d87303d55ef8c92bcad4dd3f978b26d272013642b029425775f5bad1009fe7b2" \
    "https://www.netfilter.org/pub/iptables/iptables-1.8.11.tar.xz"
mirror "xtables/$xtables_version" "k3s-root-$xtables_version.tar.gz" \
    "ab4ddff445f4aa19add06f1a53d9f1c8194b65f5e31ca54ab2abb67036bf442f" \
    "https://github.com/k3s-io/k3s-root/archive/refs/tags/$xtables_version.tar.gz"
mirror "xtables/$xtables_version" "buildroot-2025.02.14.tar.gz" \
    "8133a06142f6eb0177726b54a948b46289ebe48f9cbcaac5403cffd1a3cc9f36" \
    "https://github.com/buildroot/buildroot/archive/refs/tags/2025.02.14.tar.gz"

# mke2fs: the e2fsprogs tarball gokrazy's recipe builds (the .gz
# variant their Dockerfile names), plus glibc, which that recipe
# links in statically from its debian:bullseye toolchain. The GNU
# release tarball stands in for Debian's patched package because the
# recipe doesn't pin a package revision; NOTICES.md records that
# fidelity note.
# Tracks e2fsprogs/VERSION and the gokrazy commit in its fetch.sh.
mirror "e2fsprogs/$e2fsprogs_version" "e2fsprogs-$e2fsprogs_version.tar.gz" \
    "0d2e0bf80935c3392b73a60dbff82d8a6ef7ea88b806c2eea964b6837d3fd6c2" \
    "https://mirrors.edge.kernel.org/pub/linux/kernel/people/tytso/e2fsprogs/v$e2fsprogs_version/e2fsprogs-$e2fsprogs_version.tar.gz"
mirror "e2fsprogs/$e2fsprogs_version" "glibc-2.31.tar.xz" \
    "9246fe44f68feeec8c666bb87973d590ce0137cca145df014c72ec95be9ffd17" \
    "https://ftp.gnu.org/gnu/libc/glibc-2.31.tar.xz"

# The iSCSI and NFS clients are built from source by their own
# domains, so their tarballs are already fetched and verified against
# those domains' pins; --sources-only performs the downloads without
# the container builds.
"$here/../open-iscsi/fetch.sh" --sources-only
for tarball in "$here/../open-iscsi/cache/$openiscsi_version"/*.tar.*; do
    place "open-iscsi/$openiscsi_version" "$(basename "$tarball")" "$tarball"
done
"$here/../nfs-utils/fetch.sh" --sources-only
for tarball in "$here/../nfs-utils/cache/$nfsutils_version"/*.tar.*; do
    place "nfs-utils/$nfsutils_version" "$(basename "$tarball")" "$tarball"
done

# The static libraries those two builds link from their pinned alpine
# container: musl (every binary's libc) and util-linux (libblkid and
# libmount). Alpine patches its packages lightly; these upstream
# tarballs match the packaged versions, and NOTICES.md names the
# container digest for anyone auditing the exact bytes.
# Tracks the builder pin in open-iscsi/fetch.sh and nfs-utils/fetch.sh.
mirror "toolchain/alpine-3.22" "musl-1.2.5.tar.gz" \
    "a9a118bbe84d8764da0ea0d28b3ab3fae8477fc7e4085d90102b8596fc7c75e4" \
    "https://musl.libc.org/releases/musl-1.2.5.tar.gz"
mirror "toolchain/alpine-3.22" "util-linux-2.41.tar.xz" \
    "81ee93b3cfdfeb7d7c4090cedeba1d7bbce9141fd0b501b686b3fe475ddca4c6" \
    "https://www.kernel.org/pub/linux/utils/util-linux/v2.41/util-linux-2.41.tar.xz"

# systemd-boot and GRUB are prebuilt Ubuntu packages, so their
# corresponding source is the Ubuntu source package: the upstream
# tarball, the packaging tarball with Ubuntu's patches, and the .dsc
# that ties them together. Launchpad keeps every version forever,
# unlike the pool the binaries come from, but the mirror means nobody
# has to trust that.
# Track systemd-boot/VERSION and grub/VERSION.
launchpad="https://launchpad.net/ubuntu/+archive/primary/+sourcefiles"
mirror "systemd-boot/$systemdboot_version" "systemd_259.5.orig.tar.gz" \
    "80ed55a8a69c4bd1fb12a36659303372b37baf9ee224ef4f032db4b748be0f76" \
    "$launchpad/systemd/$systemdboot_version/systemd_259.5.orig.tar.gz"
mirror "systemd-boot/$systemdboot_version" "systemd_$systemdboot_version.debian.tar.xz" \
    "a3a1d6e6bd1edf972badef67c85425206a727329f42070db3f469b149df2619c" \
    "$launchpad/systemd/$systemdboot_version/systemd_$systemdboot_version.debian.tar.xz"
mirror "systemd-boot/$systemdboot_version" "systemd_$systemdboot_version.dsc" \
    "b8168448fa8307117663ce6a7aeee8ccddf5f736b1de70eabaefff04779bf731" \
    "$launchpad/systemd/$systemdboot_version/systemd_$systemdboot_version.dsc"
mirror "grub/$grub_version" "grub2_2.12.orig.tar.xz" \
    "f3c97391f7c4eaa677a78e090c7e97e6dc47b16f655f04683ebd37bef7fe0faa" \
    "$launchpad/grub2/$grub_version/grub2_2.12.orig.tar.xz"
mirror "grub/$grub_version" "grub2_$grub_version.debian.tar.xz" \
    "1e78cbb97d86461e8cb4789658cdeffeef32a784a006253dcc3b1b97d7056338" \
    "$launchpad/grub2/$grub_version/grub2_$grub_version.debian.tar.xz"
mirror "grub/$grub_version" "grub2_$grub_version.dsc" \
    "e23bd4184ea731a890c338fbc8b73e18d53cf4c47b0e8dffd6a55b0d13798ffa" \
    "$launchpad/grub2/$grub_version/grub2_$grub_version.dsc"

# The CA bundle: MPL-2.0, whose obligation is to keep the file's own
# source form available, and the PEM is that form.
place "trust/$trust_version" "cacert-$trust_version.pem" \
    "$here/../trust/dist/$trust_version/cacert.pem"

echo
echo "sources mirrored:"
(cd "$out" && find . -type f | sort | sed 's|^\./|  |')
