#!/usr/bin/env bash
#
# Report the pins that exist only in the source mirror.
#
# Most of what sources.sh mirrors is keyed to a domain's own VERSION
# file, and that domain's latest.sh watches it. Two pins have no
# domain of their own, because liken never fetches them: they are the
# corresponding source for code that arrives already compiled inside
# something else. iptables is what the k3s-root release built, and
# buildroot is what built it. The static programs that liken/userland
# builds need no pin here, because buildroot's legal-info names their
# sources and the libraries that they link.
#
# Neither of the two moves on its own. Each one follows the k3s-root
# release in xtables/VERSION, and this report asks that release what
# it ships now. So a row that says "behind" here means the pin it
# follows already moved, and the mirror has not caught up. Both
# channel columns print `-`.
#
# There is no independent bump. The version is part of the filename
# that the channel serves, so a new version is a new URL, and
# sources.sh must be edited by a person. --bump runs the re-pin
# instead, which is the one automatic step available: it moves every
# digest whose bytes changed under an unchanged URL.
#
# Usage:
#   licensing/latest.sh          report the two source-only pins
#   licensing/latest.sh --bump   re-pin the digests that moved

set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source-path=SCRIPTDIR source=../../toolchains/pin.sh
source "$here/../../toolchains/pin.sh"

# The mirror names each file, and the version is inside the name, so
# the pins are read from the same lines the channel serves.
pin_of() {
    grep -oE "\"$1-[0-9][^\"]*\.tar\.[a-z]+\"" "$here/sources.sh" |
        head -1 | sed -e "s|\"$1-||" -e 's|\.tar\.[a-z]*"$||'
}

pinned_iptables="$(pin_of iptables)"
pinned_buildroot="$(pin_of buildroot)"

# iptables and buildroot come from a chain: the k3s-root release liken
# pins names a buildroot version in its download script, and that
# buildroot tag names the iptables version in the package makefile.
# k3s-root replaces some of buildroot's packages with its own, but not
# this one, so buildroot's makefile is the answer.
k3s_root="$(cat "$here/../xtables/VERSION")"
latest_buildroot="$(fetch_text "https://raw.githubusercontent.com/k3s-io/k3s-root/$k3s_root/scripts/download" |
    sed -n 's|.*BUILDROOT_VERSION:=\([^}]*\)}.*|\1|p' | head -1)" || latest_buildroot=""
latest_iptables=""
if [[ -n "$latest_buildroot" ]]; then
    latest_iptables="$(fetch_text "https://raw.githubusercontent.com/buildroot/buildroot/$latest_buildroot/package/iptables/iptables.mk" |
        sed -n 's|^IPTABLES_VERSION *= *||p' | head -1)" || latest_iptables=""
fi

row iptables "$pinned_iptables" "$latest_iptables" - - - \
    "follows xtables: what k3s-root $k3s_root builds"
row buildroot "$pinned_buildroot" "$latest_buildroot" - - - \
    "follows xtables: what k3s-root $k3s_root builds with"

[[ "${1:-}" == "--bump" ]] || exit 0

exec "$here/sources.sh" --repin
