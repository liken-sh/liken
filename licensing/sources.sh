#!/usr/bin/env bash
#
# Mirror the corresponding source for everything that liken
# redistributes.
#
# liken's own code uses the MIT license, but a release also carries
# other people's work. The kernel, the netfilter tools, mke2fs, the
# iSCSI and NFS clients, systemd-boot, and GRUB all use the GPL or
# LGPL license. These licenses attach one real obligation to
# redistributing their binaries: whoever gets the binary must be able
# to get the source that built it. The GPL has a clause written for
# exactly a release channel's shape: distributing object code "from a
# designated place" meets the license when the source is offered from
# the same place. So the channel that serves the binaries also serves
# the sources, under sources/<component>/<version>/, and this script
# assembles that tree.
#
# This script mirrors the source instead of linking to it. Upstream
# URLs rot: Ubuntu's pool returns a 404 error for a package on the day
# a newer version replaces it, as the grub and systemd-boot fetch
# scripts already document. A source offer that can rot is not a real
# offer. Mirrors are keyed by component version, not release version,
# because sources only change when a pin changes. Every release built
# from the same pins shares the same mirrored sources.
#
# Every mirrored file is pinned here by its sha256 digest, the same
# discipline that every fetch script in this repository follows. Some
# pins track another domain's pin and must move together with it.
# Each pin below names the pin it follows. If a domain pin changes
# without a matching update here, the digest or URL check fails.
# Because of this check, a release cannot silently ship binaries
# whose sources are not mirrored.
#
# Some components need no mirror. k3s uses the Apache-2.0 license,
# which requires notices, not source, but its release page carries
# the source anyway. The Flux engine seed embedded in the cluster
# operator is Apache-2.0 as well, and the Flux project serves its
# source at https://github.com/fluxcd/flux2. The GPL-licensed
# userland embedded inside the
# k3s binary is built by the same k3s-root recipe mirrored under
# xtables/. The CA bundle is its own source: the PEM file is the
# preferred form for editing it, so the mirror is a copy of the
# artifact itself. CPU microcode has no source form at all: the
# vendors publish opaque binary updates whose licenses permit
# redistribution and forbid modification, so notices are the whole
# obligation (licensing/NOTICES.md carries them).
#
# Usage:
#   licensing/sources.sh            mirror the sources for the pinned
#                                   versions
#   licensing/sources.sh --repin    write the digests that a domain's
#                                   version bump moved
#
# Results land in licensing/dist/sources/<component>/<version>/, laid
# out exactly as the channel serves them at
# https://releases.liken.sh/sources/. Downloads are cached in
# licensing/cache/.
#
# The --repin mode exists because the pins above track other domains,
# and a domain that bumps its VERSION moves the source that belongs to
# it. That mode fetches each pinned URL, and where the bytes no longer
# match, it writes the new digest into this file. A sha256 string
# appears once here, so the rewrite is exact.
#
# It refuses one case on purpose. Several URLs carry a version inside
# the filename, so a bump changes the name and not only the bytes, and
# the fetch returns a 404 error. The mode reports that URL and stops.
# The mirror path is what the source offer depends on, and a guess
# there ships a release whose sources cannot be downloaded.

set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

repin=0
if [[ "${1:-}" == "--repin" ]]; then
    repin=1
fi

for tool in curl sha256sum; do
    command -v "$tool" >/dev/null || {
        echo "sources.sh: missing required tool: $tool" >&2
        exit 1
    }
done

# The component versions come from the domains that pin them. Because
# of this, the script can never disagree with what a release actually
# carries.
kernel_version="$(cat "$here/../kernel/VERSION")"
xtables_version="$(cat "$here/../xtables/VERSION")"
trust_version="$(cat "$here/../trust/VERSION")"
e2fsprogs_version="$(cat "$here/../e2fsprogs/VERSION")"
openiscsi_version="$(cat "$here/../open-iscsi/VERSION")"
nfsutils_version="$(cat "$here/../nfs-utils/VERSION")"
wpasupplicant_version="$(cat "$here/../wpa-supplicant/VERSION")"
systemdboot_version="$(cat "$here/../systemd-boot/VERSION")"
grub_version="$(cat "$here/../grub/VERSION")"
hwdata_version="$(cat "$here/../hwdata/VERSION")"
tzdata_version="$(cat "$here/../tzdata/VERSION")"
linuxfirmware_version="$(cat "$here/../linux-firmware/VERSION")"
# The regulatory database is a nested pin inside the linux-firmware
# domain, so its version comes out of that domain's fetch script
# instead of a VERSION file.
regdb_version="$(sed -n 's|^regdb_version="\(.*\)"$|\1|p' "$here/../linux-firmware/fetch.sh")"

cache="$here/cache"
out="$here/dist/sources"

# This is the channel that this mirror ultimately serves. Sources are
# keyed by component version and published exactly once. The script
# verifies each file against its pin on the way in, so a file the
# channel already serves needs no work at all: no download, no
# upload. This check keeps the script cheap on the release path,
# because the pins change far less often than anything else in a
# release, so most runs skip every file here. The check needs no
# credentials, because every published object is public-read. The
# check fails safe: if the check itself cannot reach the channel, the
# script fetches and uploads the file again rather than ever leaving
# it missing.
channel="https://releases.liken.sh"

published() {
    curl -fsI --retry 3 "$channel/sources/$1/$2" >/dev/null 2>&1
}

# repin_one <component>/<version> <filename> <sha256> <url>: what
# mirror does in --repin mode. It ignores the channel, because a file
# the channel already serves is a file whose pin is already right, and
# the point here is the one that is not. The cache is keyed by
# version, so a bumped version is always a fresh download and an
# unchanged one costs a local hash.
repins=()
repin_one() {
    local dir="$1" file="$2" sha="$3" url="$4" bytes
    mkdir -p "$cache/$dir"
    if [[ ! -f "$cache/$dir/$file" ]]; then
        echo "fetching $dir/$file"
        curl -fsSL --retry 5 --retry-delay 5 "$url" -o "$cache/$dir/$file.partial" || {
            echo "sources.sh: $url does not resolve" >&2
            echo "sources.sh: this URL carries a version in its name, so the" >&2
            echo "bump changed the name; edit the entry in this file by hand" >&2
            exit 1
        }
        mv "$cache/$dir/$file.partial" "$cache/$dir/$file"
    fi
    bytes="$(sha256sum "$cache/$dir/$file" | cut -d' ' -f1)"
    if [[ "$bytes" != "$sha" ]]; then
        repins+=(-e "s|$sha|$bytes|")
        echo "re-pinned $dir/$file"
    fi
}

# In --repin mode this script does one job: bring its own digests up
# to the bytes that the pinned URLs serve now. So the domains that
# mirror from their own cache are not fetched, and place() returns
# without doing anything. Nothing here needs a built domain, and
# nothing here writes into dist.
prefetch() { ((repin)) || "$@"; }

# mirror <component>/<version> <filename> <sha256> <url>: download
# the file once into the cache, verify it against the pin every time,
# and place it in the tree the channel serves. Hardlinks keep the big
# tarballs from existing twice on disk. The retries matter here more
# than in most fetch scripts, because this function pulls from
# several upstreams in a row (kernel.org, GitHub, GNU, Launchpad).
# Without the retries, a single transient 502 error from any of them
# would fail a release.
mirror() {
    local dir="$1" file="$2" sha="$3" url="$4"
    if ((repin)); then
        repin_one "$dir" "$file" "$sha" "$url"
        return
    fi
    if published "$dir" "$file"; then
        echo "$dir/$file is already on the channel"
        return
    fi
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

# place <component>/<version> <filename> <path>: places a file that
# another domain already fetched and verified against its own pin.
# This function only needs to lay the file out in the sources tree,
# and not even that when the channel already serves it.
place() {
    local dir="$1" file="$2" src="$3"
    # A placed file carries no digest here, so --repin has nothing to
    # write for it.
    if ((repin)); then
        return
    fi
    if published "$dir" "$file"; then
        echo "$dir/$file is already on the channel"
        return
    fi
    [[ -f "$src" ]] || {
        echo "sources.sh: $src is missing; build its domain first" >&2
        exit 1
    }
    mkdir -p "$out/$dir"
    ln -f "$src" "$out/$dir/$file" 2>/dev/null || cp "$src" "$out/$dir/$file"
}

# The kernel: the upstream tarball that Canonical's mainline archive
# builds from. Canonical's builds are unmodified, so Linus's tarball
# is the whole source. This also mirrors the exact build
# configuration, which the GPL counts as part of the corresponding
# source, and which the kernel domain already extracts from the
# module package.
# Tracks kernel/VERSION.
mirror "kernel/$kernel_version" "linux-$kernel_version.tar.xz" \
    "22a0196b3cbcdf34dc27b77561f4d040585fd3447edc9ab3531a1ac79e3041e7" \
    "https://cdn.kernel.org/pub/linux/kernel/v${kernel_version%%.*}.x/linux-$kernel_version.tar.xz"
place "kernel/$kernel_version" "config" "$here/../kernel/dist/$kernel_version/config"

# The xtables binaries are iptables, built by k3s-root's buildroot
# recipe. This mirrors three files: the program's own source, the
# recipe, and buildroot itself, which carries the build scripts and
# pins every remaining package it builds by hash. The same three
# files are the source for the GPL-licensed userland embedded in the
# k3s binary, which k3s assembles from the same k3s-root version.
# iptables tracks what k3s-root builds (xtables-legacy-multi
# --version prints the version); buildroot tracks k3s-root's
# scripts/download.
mirror "xtables/$xtables_version" "iptables-1.8.11.tar.xz" \
    "d87303d55ef8c92bcad4dd3f978b26d272013642b029425775f5bad1009fe7b2" \
    "https://www.netfilter.org/pub/iptables/iptables-1.8.11.tar.xz"
mirror "xtables/$xtables_version" "k3s-root-$xtables_version.tar.gz" \
    "ab4ddff445f4aa19add06f1a53d9f1c8194b65f5e31ca54ab2abb67036bf442f" \
    "https://github.com/k3s-io/k3s-root/archive/refs/tags/$xtables_version.tar.gz"
mirror "xtables/$xtables_version" "buildroot-2025.02.14.tar.gz" \
    "8133a06142f6eb0177726b54a948b46289ebe48f9cbcaac5403cffd1a3cc9f36" \
    "https://github.com/buildroot/buildroot/archive/refs/tags/2025.02.14.tar.gz"

# mke2fs: the e2fsprogs tarball that gokrazy's recipe builds (the .gz
# variant that their Dockerfile names), plus glibc, which that recipe
# links in statically from its debian:bullseye toolchain. The GNU
# release tarball takes the place of Debian's patched package,
# because the recipe does not pin a package revision. NOTICES.md
# records that fidelity note.
# Tracks e2fsprogs/VERSION and the gokrazy commit in its fetch.sh.
mirror "e2fsprogs/$e2fsprogs_version" "e2fsprogs-$e2fsprogs_version.tar.gz" \
    "0d2e0bf80935c3392b73a60dbff82d8a6ef7ea88b806c2eea964b6837d3fd6c2" \
    "https://mirrors.edge.kernel.org/pub/linux/kernel/people/tytso/e2fsprogs/v$e2fsprogs_version/e2fsprogs-$e2fsprogs_version.tar.gz"
mirror "e2fsprogs/$e2fsprogs_version" "glibc-2.31.tar.xz" \
    "9246fe44f68feeec8c666bb87973d590ce0137cca145df014c72ec95be9ffd17" \
    "https://ftp.gnu.org/gnu/libc/glibc-2.31.tar.xz"

# The iSCSI and NFS clients are built from source by their own
# domains, so their tarballs are already fetched and verified against
# those domains' pins. The --sources-only flag downloads the sources
# without running the container builds.
prefetch "$here/../open-iscsi/fetch.sh" --sources-only
for tarball in "$here/../open-iscsi/cache/$openiscsi_version"/*.tar.*; do
    place "open-iscsi/$openiscsi_version" "$(basename "$tarball")" "$tarball"
done
prefetch "$here/../nfs-utils/fetch.sh" --sources-only
for tarball in "$here/../nfs-utils/cache/$nfsutils_version"/*.tar.*; do
    place "nfs-utils/$nfsutils_version" "$(basename "$tarball")" "$tarball"
done

# The wireless supplicant is built from source the same way. Its BSD
# license asks for notices rather than source, so this mirror is the
# channel's own rule, not an obligation: the channel offers source for
# everything a release carries.
prefetch "$here/../wpa-supplicant/fetch.sh" --sources-only
for tarball in "$here/../wpa-supplicant/cache/$wpasupplicant_version"/*.tar.*; do
    place "wpa-supplicant/$wpasupplicant_version" "$(basename "$tarball")" "$tarball"
done

# These are the static libraries that those three builds link from
# their pinned alpine container: musl (every binary's libc) and
# util-linux (libblkid and libmount). Alpine patches its packages
# lightly. These upstream tarballs match the packaged versions, and
# NOTICES.md names the container digest for anyone who wants to audit
# the exact bytes.
# libnl is the third library: LGPL-2.1, linked into the wireless
# supplicant, and the only way a program speaks nl80211.
# Tracks the builder pin in open-iscsi/fetch.sh, nfs-utils/fetch.sh,
# and wpa-supplicant/fetch.sh.
mirror "toolchain/alpine-3.22" "musl-1.2.5.tar.gz" \
    "a9a118bbe84d8764da0ea0d28b3ab3fae8477fc7e4085d90102b8596fc7c75e4" \
    "https://musl.libc.org/releases/musl-1.2.5.tar.gz"
mirror "toolchain/alpine-3.22" "util-linux-2.41.tar.xz" \
    "81ee93b3cfdfeb7d7c4090cedeba1d7bbce9141fd0b501b686b3fe475ddca4c6" \
    "https://www.kernel.org/pub/linux/utils/util-linux/v2.41/util-linux-2.41.tar.xz"
mirror "toolchain/alpine-3.22" "libnl-3.11.0.tar.gz" \
    "2a56e1edefa3e68a7c00879496736fdbf62fc94ed3232c0baba127ecfa76874d" \
    "https://github.com/thom311/libnl/releases/download/libnl3_11_0/libnl-3.11.0.tar.gz"

# systemd-boot and GRUB are prebuilt Ubuntu packages, so their
# corresponding source is the Ubuntu source package: the upstream
# tarball, the packaging tarball with Ubuntu's patches, and the .dsc
# file that ties them together. Launchpad keeps every version
# forever, unlike the pool that the binaries come from, but the
# mirror means nobody has to depend on that.
# Track systemd-boot/VERSION and grub/VERSION.
launchpad="https://launchpad.net/ubuntu/+archive/primary/+sourcefiles"
mirror "systemd-boot/$systemdboot_version" "systemd_259.5.orig.tar.gz" \
    "80ed55a8a69c4bd1fb12a36659303372b37baf9ee224ef4f032db4b748be0f76" \
    "$launchpad/systemd/$systemdboot_version/systemd_259.5.orig.tar.gz"
mirror "systemd-boot/$systemdboot_version" "systemd_$systemdboot_version.debian.tar.xz" \
    "34048bc4b67dce61835a5a959d903f912d68bb09a93ba654e6ae33410c74aaa9" \
    "$launchpad/systemd/$systemdboot_version/systemd_$systemdboot_version.debian.tar.xz"
mirror "systemd-boot/$systemdboot_version" "systemd_$systemdboot_version.dsc" \
    "c4c764e0b98b7840619f16f42fb8229a344bc2946bd1a74612280d5e218f8177" \
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
# source form available. The PEM file is that form.
place "trust/$trust_version" "cacert-$trust_version.pem" \
    "$here/../trust/dist/$trust_version/cacert.pem"

# The PCI naming database: dual-licensed, redistributed under its
# 3-clause BSD option, which asks only for notices. The file is its
# own source form, like the CA bundle, so mirroring it costs only one
# small file. This keeps the channel's rule simple: for everything a
# release carries, the channel can give you the source.
place "hwdata/$hwdata_version" "pci.ids" \
    "$here/../hwdata/dist/$hwdata_version/pci.ids"

# The timezone database: IANA's code and data tarballs, already
# fetched and verified by the tzdata domain. The tz project puts its
# files in the public domain, so no license requires this mirror.
# liken ships compiled zone files, and these tarballs plus the zic
# flags in tzdata/fetch.sh are what reproduces them. The detached
# signatures come along so a downloader can run the same check.
prefetch "$here/../tzdata/fetch.sh" --sources-only
for source in "$here/../tzdata/cache/$tzdata_version"/*.tar.gz*; do
    place "tzdata/$tzdata_version" "$(basename "$source")" "$source"
done

# The driver firmware: the upstream release tarball, already fetched
# and verified by the linux-firmware domain. Most blobs are
# redistributable binaries whose blob is its own source form, but a
# few carry the GPL, and where their source exists upstream keeps it
# in this same tree. Mirroring the one tarball satisfies the source
# offer for all of them at once, and the WHENCE file inside it is the
# per-blob record of terms. The tarball lives in that domain's cache,
# which CI does not keep between runs, so this entry re-runs the
# domain's own verifying fetch when the tarball is absent and the
# channel does not already serve it.
linuxfirmware_tarball="linux-firmware-$linuxfirmware_version.tar.xz"
if ! ((repin)) &&
    ! published "linux-firmware/$linuxfirmware_version" "$linuxfirmware_tarball" &&
    [[ ! -f "$here/../linux-firmware/cache/$linuxfirmware_tarball" ]]; then
    "$here/../linux-firmware/fetch.sh"
fi
place "linux-firmware/$linuxfirmware_version" "$linuxfirmware_tarball" \
    "$here/../linux-firmware/cache/$linuxfirmware_tarball"

# The regulatory database. The ISC license asks for notices, not
# source, but db.txt is the text form that regulatory.db compiles
# from, and the tarball carries it along with the compiler and the
# public certificate. The whole mirror is 32 KB, so it costs nothing
# to keep the channel's rule whole. Tracks the regdb pin in
# linux-firmware/fetch.sh.
mirror "wireless-regdb/$regdb_version" "wireless-regdb-$regdb_version.tar.xz" \
    "8a27bfc081bafed8c24dd70fab0d96f098e5a0bfcd08d3da672595f225ab8993" \
    "https://mirrors.edge.kernel.org/pub/software/network/wireless-regdb/wireless-regdb-$regdb_version.tar.xz"

echo
if ((repin)); then
    # The edits are applied in one pass at the end, and not as each
    # one is found, so a run that stops on an unresolvable URL leaves
    # this file exactly as it was.
    if ((${#repins[@]})); then
        sed -i "${repins[@]}" "$here/sources.sh"
        # Each edit is two array elements, the -e and its expression.
        moved=$((${#repins[@]} / 2))
        if ((moved == 1)); then
            echo "1 source digest moved in licensing/sources.sh"
        else
            echo "$moved source digests moved in licensing/sources.sh"
        fi
    else
        echo "every source pin already matches its bytes"
    fi
elif [[ -d "$out" ]]; then
    echo "sources to publish:"
    (cd "$out" && find . -type f | sort | sed 's|^\./|  |')
else
    echo "every source is already on the channel"
fi
