#!/usr/bin/env bash
#
# Report what this domain pins, and what k3s-root has released now.
#
# k3s-root is the buildroot recipe that k3s itself uses to build the
# userland it embeds. liken takes the same release, so that the
# iptables binaries on the host and the ones inside k3s come from one
# build.
#
# This pin carries no digest. Each k3s-root release publishes a sha256
# manifest per architecture, and xtables/fetch.sh reads it on every
# fetch, so a bump here is one line in VERSION.
#
# A bump moves what iptables version liken redistributes, and the
# source mirror pins that version by name. So the bump ends by
# re-pinning the licensing domain, which is where that name lives.
#
# Usage:
#   xtables/latest.sh          report the pin and the newest release
#   xtables/latest.sh --bump   write the newest release into VERSION

set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

repository="https://github.com/k3s-io/k3s-root"

pinned="$(cat "$here/VERSION")"

latest="$(git ls-remote --tags --refs "$repository" |
    sed 's|.*refs/tags/||' |
    grep -E '^v[0-9]+\.[0-9]+\.[0-9]+$' |
    sort -V | tail -1)" || latest=""

printf '%s\t%s\t%s\t%s\n' xtables "$pinned" "${latest:-?}" \
    "k3s-root, the recipe k3s builds its own userland from"

[[ "${1:-}" == "--bump" ]] || exit 0

[[ -n "$latest" ]] || {
    echo "latest.sh: the tag list did not answer" >&2
    exit 1
}
[[ "$latest" != "$pinned" ]] || {
    echo "xtables (k3s-root) $pinned is current"
    exit 0
}

echo "$latest" >"$here/VERSION"
echo "xtables: $pinned -> $latest"
echo "the release's sha256 manifest stands behind these bytes"
# A refused re-pin leaves the domain pin moved and the mirror
# behind it, which is a release that cannot serve its own
# sources. Say that plainly, because the fix is a hand edit.
"$here/../licensing/sources.sh" --repin || {
    echo "the pin moved; licensing/sources.sh still needs a hand edit" >&2
    exit 1
}
echo "next: make -C .. xtables"
