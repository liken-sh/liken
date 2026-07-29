#!/usr/bin/env bash
#
# Report what this domain pins, and what Flux has released now.
#
# The tag list comes from git, not from the GitHub API. A tag query
# needs no token and has no rate limit, and the release this domain
# fetches is named by its tag anyway. Flux tags release candidates in
# the same namespace (v2.9.0-rc.1), so the filter keeps only the three
# numbers.
#
# This pin carries no digest. Each Flux release publishes a checksums
# file, and flux/fetch.sh reads it on every fetch, so a bump here is
# one line in VERSION.
#
# Usage:
#   flux/latest.sh          report the pin and the newest release
#   flux/latest.sh --bump   write the newest release into VERSION

set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

repository="https://github.com/fluxcd/flux2"

pinned="$(cat "$here/VERSION")"

latest="$(git ls-remote --tags --refs "$repository" |
    sed 's|.*refs/tags/||' |
    grep -E '^v[0-9]+\.[0-9]+\.[0-9]+$' |
    sort -V | tail -1)" || latest=""

printf '%s\t%s\t%s\t%s\n' flux "$pinned" "${latest:-?}" \
    "newest release tag, no release candidates"

[[ "${1:-}" == "--bump" ]] || exit 0

[[ -n "$latest" ]] || {
    echo "latest.sh: the tag list did not answer" >&2
    exit 1
}
[[ "$latest" != "$pinned" ]] || {
    echo "flux $pinned is current"
    exit 0
}

echo "$latest" >"$here/VERSION"
echo "flux: $pinned -> $latest"
echo "the release's checksums file stands behind these bytes"
echo "next: make -C .. cluster-operator"
