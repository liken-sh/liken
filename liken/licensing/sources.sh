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
# shellcheck source-path=SCRIPTDIR source=../../toolchains/pin.sh
source "$here/../../toolchains/pin.sh"

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
trust_version="$(cat "$here/../../trust/VERSION")"
userland_commit="$(cat "$here/../userland/VERSION")"
systemdboot_version="$(cat "$here/../systemd-boot/VERSION")"
grub_version="$(cat "$here/../grub/VERSION")"
hwdata_version="$(cat "$here/../hwdata/VERSION")"
linuxfirmware_version="$(cat "$here/../linux-firmware/VERSION")"
# The regulatory database is a nested pin inside the linux-firmware
# domain, so its version comes out of that domain's manifest instead
# of a VERSION file.
regdb_manifest="$here/../linux-firmware/pin.toml"
regdb_version="$(pin_value wireless-regdb version "$regdb_manifest")"

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

# repin_one <component>/<version> <filename> <sha256> <url>: what
# mirror does in --repin mode. It ignores the channel, because a file
# the channel already serves is a file whose pin is already right, and
# the point here is the one that is not. A cached file that matches
# its pin costs a local hash. Any other cached file is downloaded
# again before it is hashed, because the new digest must come from the
# bytes that the URL serves now. A cached file that a broken download
# or a disk fault changed would otherwise become the pin.
repins=()
repin_one() {
    local dir="$1" file="$2" sha="$3" url="$4" bytes
    mkdir -p "$cache/$dir"
    if sha256sum --check --status <<<"$sha  $cache/$dir/$file" >/dev/null 2>&1; then
        return
    fi
    echo "fetching $dir/$file"
    curl -fsSL --retry 5 --retry-delay 5 --max-time 1800 "$url" -o "$cache/$dir/$file.partial" || {
        rm -f "$cache/$dir/$file.partial"
        echo "sources.sh: $url does not resolve" >&2
        echo "sources.sh: this URL carries a version in its name, so the" >&2
        echo "bump changed the name; edit the entry in this file by hand" >&2
        exit 1
    }
    mv "$cache/$dir/$file.partial" "$cache/$dir/$file"
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
# the file into the cache when the cache has no good copy, verify it
# against the pin every time, and place it in the tree the channel
# serves. Hardlinks keep the big tarballs from existing twice on disk.
# The retries matter here more than in most fetch scripts, because
# this function pulls from
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
    # A cached file that matches its pin is good. Any other cached
    # file (missing, torn, or changed) is downloaded again. The bytes
    # go to a .partial file, which replaces the cached file only after
    # it matches the pin, and a .partial that does not match is
    # deleted, so a bad download never stays in the cache to fail
    # every later run.
    if ! sha256sum --check --status <<<"$sha  $cache/$dir/$file" >/dev/null 2>&1; then
        echo "mirroring $dir/$file"
        curl -fsSL --retry 5 --retry-delay 5 --max-time 1800 "$url" -o "$cache/$dir/$file.partial"
        sha256sum --check --quiet <<<"$sha  $cache/$dir/$file.partial" || {
            rm -f "$cache/$dir/$file.partial"
            echo "sources.sh: the bytes at $url do not match the pin of $dir/$file" >&2
            echo "sources.sh: if a domain's version bumped, run licensing/sources.sh --repin" >&2
            exit 1
        }
        mv "$cache/$dir/$file.partial" "$cache/$dir/$file"
    fi
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
    "fa595aad93734f829c963c2a90ef7c3673224fd7fdae7b49406e397cde2de231" \
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
if ! ((repin)); then
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
# mirror means nobody has to depend on that.
# Track systemd-boot/VERSION and grub/VERSION.
launchpad="https://launchpad.net/ubuntu/+archive/primary/+sourcefiles"
mirror "systemd-boot/$systemdboot_version" "systemd_261.3.orig.tar.gz" \
    "3f8d3d3969af7214bda600930e14c6a24135eb3dce1ba7f1b980b74e6dc15b72" \
    "$launchpad/systemd/$systemdboot_version/systemd_261.3.orig.tar.gz"
mirror "systemd-boot/$systemdboot_version" "systemd_$systemdboot_version.debian.tar.xz" \
    "337c25d28338d17316e52e2d2309a4b71946db7c350feb51555253bd296a29af" \
    "$launchpad/systemd/$systemdboot_version/systemd_$systemdboot_version.debian.tar.xz"
mirror "systemd-boot/$systemdboot_version" "systemd_$systemdboot_version.dsc" \
    "dbf49a9e6358233b12f706f5503bab6eceb557af22af21f321b6bc3191fed637" \
    "$launchpad/systemd/$systemdboot_version/systemd_$systemdboot_version.dsc"
mirror "grub/$grub_version" "grub2_2.14.orig.tar.xz" \
    "bc8d3c73535b8838d8c8e2654d73edc4e6ae8c8acdb45d5df5dc9a1547446d43" \
    "$launchpad/grub2/$grub_version/grub2_2.14.orig.tar.xz"
mirror "grub/$grub_version" "grub2_$grub_version.debian.tar.xz" \
    "5e8cc04300ff891370b9bdfe46f477b17694a97712f36a69c4d33ab19ab3aa91" \
    "$launchpad/grub2/$grub_version/grub2_$grub_version.debian.tar.xz"
mirror "grub/$grub_version" "grub2_$grub_version.dsc" \
    "2c697a2d159bc2494f32eafbd6fb67cddf2b3a8ae1088f8108f4276c5a600c07" \
    "$launchpad/grub2/$grub_version/grub2_$grub_version.dsc"

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
if ! ((repin)) && {
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
