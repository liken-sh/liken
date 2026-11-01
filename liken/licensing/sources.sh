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
# Every file that this script downloads is an entry of the pin.toml of
# the domain whose binary was built from it, with its url and its
# sha256, and the script reads both with pin_value and downloads with
# fetch_pinned, the same way a fetch.sh does. That domain's latest.sh
# --bump writes the entry in the same bump that moves the binary, so
# one bump moves both. `ci pins` fails when this script holds a digest,
# and when an entry's version differs from the domain's VERSION, except
# for the iptables and buildroot releases that the k3s-root release in
# xtables/VERSION names. The other files come from a domain's own
# cache, where its fetch already checked them.
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
#   licensing/sources.sh --verify   download each file that a pin.toml
#                                   entry names into the cache and
#                                   check it against its digest
#
# Results land in licensing/dist/sources/<component>/<version>/, laid
# out exactly as the channel serves them at
# https://releases.liken.sh/sources/. Downloads are cached in
# licensing/cache/.
#
# A bump takes each digest from what the upstream lists, such as a
# signed sums file, and does not always download the file. The
# --verify mode downloads every file that a pin.toml entry names, so it
# shows before a release that each URL serves the bytes of its pin. It
# does not ask the channel, it writes nothing into dist, and it uploads
# nothing.

set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source-path=SCRIPTDIR source=../../toolchains/pin.sh
source "$here/../../toolchains/pin.sh"

verify=0
case "${1:-}" in
"") ;;
--verify) verify=1 ;;
*) fail "unknown option ${1}; try --verify" ;;
esac

require_tools sha256sum

# The component versions come from the domains that pin them. Because
# of this, the script can never disagree with what a release actually
# carries.
kernel_version="$(cat "$here/../kernel/VERSION")"
xtables_version="$(cat "$here/../xtables/VERSION")"
trust_version="$(cat "$here/../../trust/VERSION")"
userland_commit="$(cat "$here/../userland/VERSION")"
systemdboot_version="$(cat "$here/../systemd-boot/VERSION")"
grub_version="$(cat "$here/../grub/VERSION")"
hwdata_version="$(cat "$here/../hwdata/VERSION")"
linuxfirmware_version="$(cat "$here/../linux-firmware/VERSION")"
# The regulatory database is a nested pin inside the linux-firmware
# domain, so its version comes out of that domain's manifest instead
# of a VERSION file.
regdb_version="$(pin_value wireless-regdb version "$here/../linux-firmware/pin.toml")"

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
    curl -fsI --retry 3 --max-time 60 "$channel/sources/$1/$2" >/dev/null 2>&1
}

# In --verify mode this script does one job: download each file that
# a pin.toml entry names and check it. So the domains that mirror from
# their own cache are not fetched, and place() returns without doing
# anything. Nothing here needs a built domain, and nothing here writes
# into dist.
prefetch() { ((verify)) || "$@"; }

# mirror <component>/<version> <domain> <entry> <version> [<filename>]:
# download the file that the entry of the domain's pin.toml names into
# the cache when the cache has no good copy, and place it in the tree
# the channel serves under the filename, or under the last part of its
# url. The version is the one that the domain pins, and fetch_pinned
# stops when the entry names another. fetch_pinned checks the cached
# file against the pin every time, downloads a missing or changed one
# again, and retries a transient error, which matters here because
# this script reads several upstreams in a row: kernel.org, GitHub,
# netfilter.org, and Launchpad. Hardlinks keep the big tarballs from
# existing twice on disk.
mirror() {
    local dir="$1" manifest="$here/../$2/pin.toml" entry="$3" version="$4" file="${5:-}"
    [[ -n "$file" ]] || file="$(basename "$(pin_value "$entry" url "$manifest")")"
    if ((verify)); then
        fetch_pinned --resume "$entry" "$version" "$cache/$dir/$file" "$manifest"
        echo "$dir/$file matches $2/pin.toml"
        return
    fi
    if published "$dir" "$file"; then
        echo "$dir/$file is already on the channel"
        return
    fi
    fetch_pinned --resume "$entry" "$version" "$cache/$dir/$file" "$manifest"
    mkdir -p "$out/$dir"
    ln -f "$cache/$dir/$file" "$out/$dir/$file" 2>/dev/null \
        || cp "$cache/$dir/$file" "$out/$dir/$file"
}

# place <component>/<version> <filename> <path>: places a file that
# another domain already fetched and verified against its own pin.
# This function only needs to lay the file out in the sources tree,
# and not even that when the channel already serves it.
place() {
    local dir="$1" file="$2" src="$3"
    # A placed file came through its domain's own fetch, so --verify
    # has nothing to check for it.
    if ((verify)); then
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
mirror "kernel/$kernel_version" kernel linux-source "$kernel_version"
place "kernel/$kernel_version" "config" "$here/../kernel/dist/$kernel_version/config"

# The xtables binaries are iptables, built by k3s-root's buildroot
# recipe. This mirrors three files: the program's own source, the
# recipe, and buildroot itself, which carries the build scripts and
# pins every remaining package it builds by hash. The same three
# files are the source for the GPL-licensed userland embedded in the
# k3s binary, which k3s assembles from the same k3s-root version.
# xtables/latest.sh --bump writes all three entries from the k3s-root
# release in xtables/VERSION: the iptables and buildroot releases are
# the ones that the k3s-root release names, so their versions are in
# their entries alone. GitHub names the archive of a tag by the tag, so
# the mirror names the two archives after their projects.
iptables_version="$(pin_value iptables-source version "$here/../xtables/pin.toml")"
buildroot_version="$(pin_value buildroot-source version "$here/../xtables/pin.toml")"
mirror "xtables/$xtables_version" xtables iptables-source "$iptables_version"
mirror "xtables/$xtables_version" xtables k3s-root-source "$xtables_version" \
    "k3s-root-$xtables_version.tar.gz"
mirror "xtables/$xtables_version" xtables buildroot-source "$buildroot_version" \
    "buildroot-$buildroot_version.tar.gz"

# The static programs of the userland domain: mke2fs, mount.nfs,
# iscsid and iscsiadm, wpa_supplicant, and the time zone database.
# buildroot builds them,
# and `make legal-info` copies the source of every package that the
# build put into them, the libraries and musl included, with the
# patches that buildroot applied, into
# userland/dist/legal-info/sources/. Each package's directory there is
# <package>-<version>, and manifest.csv beside it names the package
# and the version, so each one lands at sources/<package>/<version>/.
# Every file in that tree matched a buildroot .hash file when
# buildroot downloaded it, so it carries no digest here. The scripts
# that control the build are buildroot itself, at the commit in
# userland/VERSION, which lands at sources/buildroot/<commit>/, and the
# BR2_EXTERNAL tree in liken/userland/ at the release's tag. The
# wireless supplicant and the time zone database ask for no source,
# and the channel offers it anyway: for everything a release carries,
# the channel can give you the source.
prefetch "$here/../userland/fetch.sh" --sources-only
place "buildroot/$userland_commit" "buildroot-$userland_commit.tar.gz" \
    "$here/../userland/cache/buildroot/buildroot-$userland_commit.tar.gz"
legal="$here/../userland/dist/legal-info"
if ! ((verify)); then
    [[ -f "$legal/manifest.csv" ]] || {
        echo "sources.sh: $legal/manifest.csv is missing; build the userland domain first" >&2
        exit 1
    }
    # manifest.csv quotes every field, and its first two are the
    # package and the version.
    while IFS=, read -r package version _; do
        package="${package//\"/}"
        version="${version//\"/}"
        # A release document names two components otherwise than
        # buildroot names their packages: wpa_supplicant is
        # buildroot's spelling, and open-iscsi-static is the recipe in
        # userland/iscsi/. The mirror takes the release document's
        # name, because a reader finds a component's source by the
        # name and version that it lists.
        case "$package" in
        wpa_supplicant) component=wpa-supplicant ;;
        open-iscsi-static) component=open-iscsi ;;
        *) component="$package" ;;
        esac
        for file in "$legal/sources/$package-$version"/*; do
            place "$component/$version" "$(basename "$file")" "$file"
        done
    done < <(tail -n +2 "$legal/manifest.csv")
    # zic ran on the build machine and compiled the zone files, so
    # legal-info lists its tzcode tarball among the host sources, and
    # userland/build.sh keeps that one.
    for file in "$legal/host-sources"/zic-*/*; do
        version="$(basename "$(dirname "$file")")"
        place "zic/${version#zic-}" "$(basename "$file")" "$file"
    done
fi

# systemd-boot and GRUB are prebuilt Ubuntu packages, so their
# corresponding source is the Ubuntu source package: the upstream
# tarball, the packaging tarball with Ubuntu's patches, and the .dsc
# file that ties them together. Launchpad keeps every version
# forever, unlike the pool that the binaries come from, but the
# mirror means nobody has to depend on that. Each file keeps the name
# that Launchpad serves it under, which the url of its entry ends in.
mirror "systemd-boot/$systemdboot_version" systemd-boot systemd-source-orig "$systemdboot_version"
mirror "systemd-boot/$systemdboot_version" systemd-boot systemd-source-debian "$systemdboot_version"
mirror "systemd-boot/$systemdboot_version" systemd-boot systemd-source-dsc "$systemdboot_version"
mirror "grub/$grub_version" grub grub2-source-orig "$grub_version"
mirror "grub/$grub_version" grub grub2-source-debian "$grub_version"
mirror "grub/$grub_version" grub grub2-source-dsc "$grub_version"

# The CA bundle: MPL-2.0, whose obligation is to keep the file's own
# source form available. The PEM file is that form.
place "trust/$trust_version" "cacert-$trust_version.pem" \
    "$here/../../trust/dist/$trust_version/cacert.pem"

# The PCI naming database: dual-licensed, redistributed under its
# 3-clause BSD option, which asks only for notices. The file is its
# own source form, like the CA bundle, so mirroring it costs only one
# small file. This keeps the channel's rule simple: for everything a
# release carries, the channel can give you the source.
place "hwdata/$hwdata_version" "pci.ids" \
    "$here/../hwdata/dist/$hwdata_version/pci.ids"

# The driver firmware: the upstream release tarball, already fetched
# and verified by the linux-firmware domain. Most blobs are
# redistributable binaries whose blob is its own source form, but a
# few carry the GPL, and where their source exists upstream keeps it
# in this same tree. Mirroring the one tarball satisfies the source
# offer for all of them at once, and the WHENCE file inside it is the
# per-blob record of terms.
#
# The regulatory database. The ISC license asks for notices, not
# source, but db.txt is the text form that regulatory.db compiles
# from, and the tarball carries it along with the compiler and the
# public certificate. The whole mirror is 32 KB, so it costs nothing
# to keep the channel's rule whole. The linux-firmware domain's fetch
# downloads it too, and verifies it against its entry in
# linux-firmware/pin.toml, so this script holds no digest for it.
#
# Both tarballs live in that domain's cache, which CI does not keep
# between runs, so this entry re-runs the domain's own verifying fetch
# when a tarball is absent and the channel does not already serve it.
linuxfirmware_tarball="linux-firmware-$linuxfirmware_version.tar.xz"
regdb_tarball="wireless-regdb-$regdb_version.tar.xz"
linuxfirmware_cache="$here/../linux-firmware/cache"
if ! ((verify)) && {
    {
        ! published "linux-firmware/$linuxfirmware_version" "$linuxfirmware_tarball" &&
            [[ ! -f "$linuxfirmware_cache/$linuxfirmware_tarball" ]]
    } || {
        ! published "wireless-regdb/$regdb_version" "$regdb_tarball" &&
            [[ ! -f "$linuxfirmware_cache/$regdb_tarball" ]]
    }
}; then
    "$here/../linux-firmware/fetch.sh"
fi
place "linux-firmware/$linuxfirmware_version" "$linuxfirmware_tarball" \
    "$linuxfirmware_cache/$linuxfirmware_tarball"
place "wireless-regdb/$regdb_version" "$regdb_tarball" \
    "$linuxfirmware_cache/$regdb_tarball"

echo
if ((verify)); then
    echo "every source that a pin.toml names matches its digest"
elif [[ -d "$out" ]]; then
    echo "sources to publish:"
    (cd "$out" && find . -type f | sort | sed 's|^\./|  |')
else
    echo "every source is already on the channel"
fi
