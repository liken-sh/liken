#!/usr/bin/env bash
#
# Report the pins that exist only in the source mirror.
#
# Most of what sources.sh mirrors is keyed to a domain's own VERSION
# file, and that domain's latest.sh watches it. Five pins have no
# domain of their own, because liken never fetches them: they are the
# corresponding source for code that arrives already compiled inside
# something else. musl and util-linux are linked into the static
# binaries that the OS builds in the alpine image. libnl is linked
# into the wireless supplicant. iptables is what the k3s-root release
# built, and buildroot is what built it.
#
# None of these five moves on its own. Each one follows something
# else, and this report asks that governing thing what it ships now.
# So a row that says "behind" here means the pin it follows already
# moved, and the mirror has not caught up.
#
# musl, util-linux, and libnl are the packages of the Alpine branch
# that the `alpine` entry in toolchains/images.toml installs from, so
# their channel is that branch, such as v3.22. Under the rule in root
# plan 83, a pin from a package repository follows the newest branch
# that the repository offers to install whole, and for Alpine that is
# `edge`. So their newest-channel column prints `edge`, and their
# latest column is the version in the branch that the image uses now,
# because the mirror must match the libraries that the build linked.
# iptables and buildroot follow the k3s-root release in
# xtables/VERSION, so both channel columns print `-`.
#
# There is no independent bump. The version is part of the filename
# that the channel serves, so a new version is a new URL, and
# sources.sh must be edited by a person. --bump runs the re-pin
# instead, which is the one automatic step available: it moves every
# digest whose bytes changed under an unchanged URL.
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

pinned_musl="$(pin_of musl)"
pinned_utillinux="$(pin_of util-linux)"
pinned_libnl="$(pin_of libnl)"
pinned_iptables="$(pin_of iptables)"
pinned_buildroot="$(pin_of buildroot)"

# The image's tag names the Alpine release, as 3.22 or 3.22.2, and
# Alpine's package repository names its branch by the first two
# numbers with a v in front, as v3.22. edge has no number.
alpine_tag="$(sed -n '/^\[build\]/,/^\[/ s|^alpine = "alpine:\([^@"]*\).*"$|\1|p' \
    "$here/../../toolchains/images.toml")"
case "$alpine_tag" in
edge) branch="edge" ;;
"") branch="?" ;;
*) branch="v$(cut -d. -f1-2 <<<"$alpine_tag")" ;;
esac
newest_branch="edge"

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
    curl -fsS --retry 3 --max-time 120 \
        "https://dl-cdn.alpinelinux.org/alpine/$branch/main/x86_64/APKINDEX.tar.gz" |
        tar -xzO APKINDEX |
        awk -v want="P:$1" -v RS='' '
            { hit = 0
              for (i = 1; i <= NF; i++) if ($i == want) hit = 1
              if (hit && !done)
                  for (i = 1; i <= NF; i++)
                      if ($i ~ /^V:/) { print substr($i, 3); done = 1 } }' |
        sed 's|-r[0-9]*$||'
}

latest_musl=""
latest_utillinux=""
latest_libnl=""
if [[ "$branch" != "?" ]]; then
    latest_musl="$(alpine_package musl)" || latest_musl=""
    latest_utillinux="$(alpine_package util-linux-dev)" || latest_utillinux=""
    latest_libnl="$(alpine_package libnl3-dev)" || latest_libnl=""
fi

# iptables and buildroot come from a chain: the k3s-root release liken
# pins names a buildroot version in its download script, and that
# buildroot tag names the iptables version in the package makefile.
# k3s-root replaces some of buildroot's packages with its own, but not
# this one, so buildroot's makefile is the answer.
k3s_root="$(cat "$here/../xtables/VERSION")"
latest_buildroot="$(curl -fsS --retry 3 --max-time 60 \
    "https://raw.githubusercontent.com/k3s-io/k3s-root/$k3s_root/scripts/download" |
    sed -n 's|.*BUILDROOT_VERSION:=\([^}]*\)}.*|\1|p' | head -1)" || latest_buildroot=""
latest_iptables=""
if [[ -n "$latest_buildroot" ]]; then
    latest_iptables="$(curl -fsS --retry 3 --max-time 60 \
        "https://raw.githubusercontent.com/buildroot/buildroot/$latest_buildroot/package/iptables/iptables.mk" |
        sed -n 's|^IPTABLES_VERSION *= *||p' | head -1)" || latest_iptables=""
fi

printf '%s\t%s\t%s\t%s\t%s\t%s\t%s\n' musl "$pinned_musl" "${latest_musl:-?}" \
    "$branch" "$newest_branch" - "follows the alpine image in toolchains/images.toml"
printf '%s\t%s\t%s\t%s\t%s\t%s\t%s\n' util-linux "$pinned_utillinux" "${latest_utillinux:-?}" \
    "$branch" "$newest_branch" - "follows the alpine image in toolchains/images.toml"
printf '%s\t%s\t%s\t%s\t%s\t%s\t%s\n' libnl "$pinned_libnl" "${latest_libnl:-?}" \
    "$branch" "$newest_branch" - "follows the alpine image in toolchains/images.toml"
printf '%s\t%s\t%s\t%s\t%s\t%s\t%s\n' iptables "$pinned_iptables" "${latest_iptables:-?}" - - - \
    "follows xtables: what k3s-root $k3s_root builds"
printf '%s\t%s\t%s\t%s\t%s\t%s\t%s\n' buildroot "$pinned_buildroot" "${latest_buildroot:-?}" - - - \
    "follows xtables: what k3s-root $k3s_root builds with"

[[ "${1:-}" == "--bump" ]] || exit 0

exec "$here/sources.sh" --repin
