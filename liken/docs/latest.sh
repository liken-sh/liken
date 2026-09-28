#!/usr/bin/env bash
#
# Report what this domain pins, and what Hugo has released now.
#
# The website is built by Hugo, the one build tool liken pins by
# version, because the site's theme and layouts are written against
# one Hugo release. The pin is a tool dependency in this domain's own
# go.mod: the standard Hugo build is pure Go, and the module system
# records the version there and the digest of every module in go.sum,
# so there is no fetch script and no digest to write by hand. The tag
# list comes from git: no token, no rate limit.
#
# Usage:
#   docs/latest.sh          report the pin and the newest release
#   docs/latest.sh --bump   move the tool dependency to the newest release

set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

repository="https://github.com/gohugoio/hugo"

# go.mod names Hugo twice: bare in the tool block, and with its
# version on a require line. The version test picks the require line.
# The report strips the v, because Hugo's releases name themselves by
# the bare number.
pinned="$(awk '$1 == "github.com/gohugoio/hugo" && $2 ~ /^v/ { sub(/^v/, "", $2); print $2 }' \
    "$here/go.mod")"

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

# go get moves the tool directive's version and writes the new
# digests into go.sum in the same step, so the version and the
# digests cannot disagree the way a hand-edited pair could.
(cd "$here" && go get -tool "github.com/gohugoio/hugo@v$latest" && go mod tidy)
echo "hugo: $pinned -> $latest"
echo "go.sum now pins the new digests"
echo "next: make -C .. docs"
