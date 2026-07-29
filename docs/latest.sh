#!/usr/bin/env bash
#
# Report what this domain pins, and what Hugo has released now.
#
# The website is built by Hugo, and this pin is the only build tool
# liken vendors by version, because the site's theme and its shortcodes
# are written against one Hugo release. The tag list comes from git:
# no token, no rate limit.
#
# Hugo publishes a checksums file with every release, so a bump reads
# the new digest from upstream rather than from its own download.
#
# Usage:
#   docs/latest.sh          report the pin and the newest release
#   docs/latest.sh --bump   write the newest release and its digest

set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

repository="https://github.com/gohugoio/hugo"

pinned="$(cat "$here/VERSION")"

# VERSION holds the bare number, because that is how the release
# names its files. The tags carry a v.
latest="$(git ls-remote --tags --refs "$repository" |
    sed 's|.*refs/tags/v||' |
    grep -E '^[0-9]+\.[0-9]+\.[0-9]+$' |
    sort -V | tail -1)" || latest=""

printf '%s\t%s\t%s\t%s\n' hugo "$pinned" "${latest:-?}" \
    "the website's build tool"

[[ "${1:-}" == "--bump" ]] || exit 0

[[ -n "$latest" ]] || {
    echo "latest.sh: the tag list did not answer" >&2
    exit 1
}
[[ "$latest" != "$pinned" ]] || {
    echo "hugo $pinned is current"
    exit 0
}

tarball="hugo_${latest}_linux-amd64.tar.gz"
sums="https://github.com/gohugoio/hugo/releases/download/v$latest/hugo_${latest}_checksums.txt"
digest="$(curl -fsSL --retry 3 "$sums" |
    awk -v t="$tarball" '$2 == t { print $1 }')"
[[ -n "$digest" ]] || {
    echo "latest.sh: no $tarball listed in $sums" >&2
    exit 1
}

echo "$latest" >"$here/VERSION"
sed -i "s|^digest=\".*\"|digest=\"$digest\"|" "$here/fetch.sh"
echo "hugo: $pinned -> $latest"
echo "the release's checksums file stands behind these bytes"
echo "next: make -C .. docs"
