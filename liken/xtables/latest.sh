#!/usr/bin/env bash
#
# Report what this domain pins, and the k3s-root release that the
# pinned k3s bundles.
#
# k3s-root is the buildroot recipe that k3s itself uses to build the
# userland it embeds. liken takes the same release, so that the
# iptables binaries on the host and the ones inside k3s come from one
# build. The newest k3s-root tag is the wrong answer: k3s takes a new
# k3s-root in a later patch release, and until then the newest tag
# differs from what k3s carries. Each k3s release names its k3s-root
# as VERSION_ROOT in scripts/version.sh, so this script reads that
# line at the tag in k3s/VERSION. A k3s bump therefore comes first,
# and this domain follows it.
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
#   xtables/latest.sh          report the pin and the release k3s bundles
#   xtables/latest.sh --bump   write the release k3s bundles into VERSION

set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

pinned="$(cat "$here/VERSION")"

# The tag carries a +, which a URL path must escape.
k3s="$(cat "$here/../k3s/VERSION")"
latest="$(curl -fsS --retry 3 \
    "https://raw.githubusercontent.com/k3s-io/k3s/${k3s/+/%2B}/scripts/version.sh" |
    sed -n 's/^VERSION_ROOT="\(v[0-9.]*\)"$/\1/p')" || latest=""

printf '%s\t%s\t%s\t%s\n' xtables "$pinned" "${latest:-?}" \
    "k3s-root, the recipe k3s $k3s builds its own userland from"

[[ "${1:-}" == "--bump" ]] || exit 0

[[ -n "$latest" ]] || {
    echo "latest.sh: k3s $k3s names no VERSION_ROOT" >&2
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
