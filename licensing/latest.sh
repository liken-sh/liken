#!/usr/bin/env bash
#
# Report the pins that exist only in the source mirror.
#
# Most of what sources.sh mirrors is keyed to a domain's own VERSION
# file, and that domain's latest.sh watches it. Five pins have no
# domain of their own, because liken never fetches them: they are the
# corresponding source for code that arrives already compiled inside
# something else. glibc is linked into the mke2fs binary. musl and
# util-linux are linked into the iSCSI and NFS binaries. iptables is
# what the k3s-root release built, and buildroot is what built it.
#
# None of these five moves on its own. Each one follows something
# else, and this report asks that governing thing what it ships now.
# So a row that says "behind" here means the pin it follows already
# moved, and the mirror has not caught up.
#
# There is no independent bump. The version is part of the filename
# that the channel serves, so a new version is a new URL, and
# sources.sh must be edited by a person. --bump runs the re-pin
# instead, which is the one automatic step available: it moves every
# digest whose bytes changed under an unchanged URL.
#
# NOTICES.md names exactly one version in its prose, glibc's, because
# that library arrives inside a binary and its notice has to say which
# one. Every other notice names a project and a license, not a
# version, so a pin bump leaves that file alone.
#
# Usage:
#   licensing/latest.sh          report the five source-only pins
#   licensing/latest.sh --bump   re-pin the digests that moved

set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# The mirror names each file, and the version is inside the name, so
# the pins are read from the same lines the channel serves.
pin_of() {
    grep -oE "\"$1-[0-9][^\"]*\.tar\.[a-z]+\"" "$here/sources.sh" |
        head -1 | sed -e "s|\"$1-||" -e 's|\.tar\.[a-z]*"$||'
}

# glibc is not liken's choice, and not the recipe's either: it is
# whatever the recipe's base image ships. Bullseye ships 2.31. So the
# row is current while that FROM line is unchanged, and unknown the
# moment it moves, because a different base means a different libc
# that nobody here has looked up yet.
bullseye_glibc="2.31"

pinned_glibc="$(pin_of glibc)"
pinned_musl="$(pin_of musl)"
pinned_utillinux="$(pin_of util-linux)"
pinned_iptables="$(pin_of iptables)"
pinned_buildroot="$(pin_of buildroot)"

# The Alpine toolchain's version is in the mirror's own path, because
# these two libraries are only ever the ones that image shipped.
alpine_tag="$(grep -oE '"toolchain/alpine-[0-9.]+"' "$here/sources.sh" |
    head -1 | sed -e 's|"toolchain/alpine-||' -e 's|"$||')"

# One index lists every package in an Alpine release, as records
# separated by a blank line, with P: for the name and V: for the
# version. The version carries Alpine's own -rN build suffix, which is
# not part of the upstream release the mirror carries.
#
# The awk reads to the end of the index instead of stopping at the
# record it wants. Stopping would close the pipe under tar, and a
# pipeline that ends in a broken pipe fails, which would report every
# package as unknown.
alpine_package() {
    curl -fsS --retry 3 \
        "https://dl-cdn.alpinelinux.org/alpine/v$alpine_tag/main/x86_64/APKINDEX.tar.gz" |
        tar -xzO APKINDEX |
        awk -v want="P:$1" -v RS='' '
            { hit = 0
              for (i = 1; i <= NF; i++) if ($i == want) hit = 1
              if (hit && !done)
                  for (i = 1; i <= NF; i++)
                      if ($i ~ /^V:/) { print substr($i, 3); done = 1 } }' |
        sed 's|-r[0-9]*$||'
}

latest_musl="$(alpine_package musl)" || latest_musl=""
latest_utillinux="$(alpine_package util-linux-dev)" || latest_utillinux=""

# iptables and buildroot come from a chain: the k3s-root release liken
# pins names a buildroot version in its download script, and that
# buildroot tag names the iptables version in the package makefile.
# k3s-root replaces some of buildroot's packages with its own, but not
# this one, so buildroot's makefile is the answer.
k3s_root="$(cat "$here/../xtables/VERSION")"
latest_buildroot="$(curl -fsS --retry 3 \
    "https://raw.githubusercontent.com/k3s-io/k3s-root/$k3s_root/scripts/download" |
    sed -n 's|.*BUILDROOT_VERSION:=\([^}]*\)}.*|\1|p' | head -1)" || latest_buildroot=""
latest_iptables=""
if [[ -n "$latest_buildroot" ]]; then
    latest_iptables="$(curl -fsS --retry 3 \
        "https://raw.githubusercontent.com/buildroot/buildroot/$latest_buildroot/package/iptables/iptables.mk" |
        sed -n 's|^IPTABLES_VERSION *= *||p' | head -1)" || latest_iptables=""
fi

# The recipe's commit is the e2fsprogs domain's pin, so the base image
# this reads is the one that domain's binary was built in.
gokrazy_commit="$(sed -n 's|^commit="\(.*\)"$|\1|p' "$here/../e2fsprogs/fetch.sh")"
base="$(curl -fsS --retry 3 \
    "https://raw.githubusercontent.com/gokrazy/mkfs/$gokrazy_commit/Dockerfile" |
    sed -n 's|^FROM ||p' | head -1)" || base=""
latest_glibc=""
if [[ "$base" == "debian:bullseye" ]]; then
    latest_glibc="$bullseye_glibc"
fi

printf '%s\t%s\t%s\t%s\n' glibc "$pinned_glibc" "${latest_glibc:-?}" \
    "follows the base image in the gokrazy recipe (${base:-?})"
printf '%s\t%s\t%s\t%s\n' musl "$pinned_musl" "${latest_musl:-?}" \
    "follows the alpine $alpine_tag builder"
printf '%s\t%s\t%s\t%s\n' util-linux "$pinned_utillinux" "${latest_utillinux:-?}" \
    "follows the alpine $alpine_tag builder"
printf '%s\t%s\t%s\t%s\n' iptables "$pinned_iptables" "${latest_iptables:-?}" \
    "follows what k3s-root $k3s_root builds"
printf '%s\t%s\t%s\t%s\n' buildroot "$pinned_buildroot" "${latest_buildroot:-?}" \
    "follows what k3s-root $k3s_root builds with"

[[ "${1:-}" == "--bump" ]] || exit 0

exec "$here/sources.sh" --repin
