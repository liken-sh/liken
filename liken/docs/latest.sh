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
# The Go module proxy serves each Hugo release as its tag names it, so
# the pin has no channel, and both channel columns print `-`. Under
# the rule in root plan 83 it follows the newest Hugo release.
#
# Usage:
#   docs/latest.sh          report the pin and the newest release
#   docs/latest.sh --bump   move the tool dependency to the newest release

set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source-path=SCRIPTDIR source=../../toolchains/pin.sh
source "$here/../../toolchains/pin.sh"

repository="https://github.com/gohugoio/hugo"

# go.mod names Hugo twice: bare in the tool block, and with its
# version on a require line. The version test picks the require line.
# The report strips the v, because Hugo's releases name themselves by
# the bare number.
pinned="$(awk '$1 == "github.com/gohugoio/hugo" && $2 ~ /^v/ { sub(/^v/, "", $2); print $2 }' \
    "$here/go.mod")"

latest="$(newest_tag "$repository" '^[0-9]+\.[0-9]+\.[0-9]+$' v)" || latest=""

row hugo "$pinned" "$latest" - - - "the website's build tool"

[[ "${1:-}" == "--bump" ]] || exit 0

bump_needed hugo "$pinned" "$latest" || exit 0

# go get moves the tool directive's version and writes the new
# digests into go.sum in the same step, so the version and the
# digests cannot disagree the way a hand-edited pair could.
(cd "$here" && go get -tool "github.com/gohugoio/hugo@v$latest" && go mod tidy)
bumped hugo "$pinned" "$latest"
echo "go.sum now pins the new digests"
next_step "make -C .. docs"
