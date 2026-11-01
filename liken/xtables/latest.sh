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
# The pin carries the tarball's digest in xtables/fetch.sh. Each
# k3s-root release publishes a sha256 manifest per architecture, and
# --bump copies the xtables tarball's line from it into fetch.sh.
#
# A bump moves what iptables version liken redistributes, and the
# source mirror pins that version by name. So the bump ends by
# re-pinning the licensing domain, which is where that name lives.
#
# This pin follows another pin, k3s, so it has no channel of its own,
# and both channel columns print `-`. Under the rule in root plan 83
# it is current when it matches the k3s-root release that the pinned
# k3s names.
#
# Usage:
#   xtables/latest.sh          report the pin and the release k3s bundles
#   xtables/latest.sh --bump   write the release k3s bundles into VERSION

set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

pinned="$(cat "$here/VERSION")"

# The tag carries a +, which a URL path must escape.
k3s="$(cat "$here/../k3s/VERSION")"
latest="$(curl -fsS --retry 3 --max-time 60 \
    "https://raw.githubusercontent.com/k3s-io/k3s/${k3s/+/%2B}/scripts/version.sh" |
    sed -n 's/^VERSION_ROOT="\(v[0-9.]*\)"$/\1/p')" || latest=""

printf '%s\t%s\t%s\t%s\t%s\t%s\t%s\n' xtables "$pinned" "${latest:-?}" - - - \
    "follows k3s $k3s: the k3s-root release it builds its userland from"

[[ "${1:-}" == "--bump" ]] || exit 0

[[ -n "$latest" ]] || {
    echo "latest.sh: k3s $k3s names no VERSION_ROOT" >&2
    exit 1
}
[[ "$latest" != "$pinned" ]] || {
    echo "xtables (k3s-root) $pinned is current"
    exit 0
}

# One sha256 manifest per architecture covers all of the release's
# artifacts, and the bump reads the line for the xtables tarball.
manifest="https://github.com/k3s-io/k3s-root/releases/download/$latest/sha256sum-amd64.txt"
digest="$(curl -fsSL --retry 3 --max-time 60 "$manifest" |
    awk '$2 == "k3s-root-xtables-amd64.tar" { print $1 }')" || digest=""
[[ "$digest" =~ ^[0-9a-f]{64}$ ]] || {
    echo "latest.sh: $manifest lists no xtables tarball" >&2
    exit 1
}

# The array in fetch.sh holds one entry, the pinned version's, so the
# bump replaces the line after the array's opening line.
echo "$latest" >"$here/VERSION"
sed -i "/^declare -A xtables_sha256=(/{n;s|.*|    [\"$latest\"]=\"$digest\"|}" "$here/fetch.sh"
echo "xtables: $pinned -> $latest"
echo "the digest came from the release's sha256 manifest"
# A refused re-pin leaves the domain pin moved and the mirror
# behind it, which is a release that cannot serve its own
# sources. Say that plainly, because the fix is a hand edit.
"$here/../licensing/sources.sh" --repin || {
    echo "the pin moved; licensing/sources.sh still needs a hand edit" >&2
    exit 1
}
echo "next: make -C .. xtables"
