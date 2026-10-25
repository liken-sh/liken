#!/usr/bin/env bash
#
# Report what this domain pins, and what the e2fsprogs maintainer has
# released now.
#
# The pin is an e2fsprogs release, and the bytes are its source
# tarball on kernel.org, which e2fsprogs/fetch.sh builds into a static
# mke2fs. `liken` builds this domain from source, so the pin has no
# channel, and both channel columns print `-`. Under the rule in root
# plan 83 it follows the newest e2fsprogs release. The image that the
# build runs in is the `alpine` entry in toolchains/images.toml, and
# toolchains/latest.sh reports it.
#
# kernel.org keeps one directory for each release, named v<version>,
# so the directory listing is the list of releases. Release candidates
# go into the testing/ directory beside them, so the listing at the
# top holds releases only.
#
# This pin carries no digest. Each release directory holds a
# sha256sums.asc file, and fetch.sh reads it on every fetch, so a bump
# here is one line in VERSION.
#
# Usage:
#   e2fsprogs/latest.sh          report the pin and the newest release
#   e2fsprogs/latest.sh --bump   write the newest release into VERSION

set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

archive="https://www.kernel.org/pub/linux/kernel/people/tytso/e2fsprogs"

pinned="$(cat "$here/VERSION")"

latest="$(curl -fsS --retry 3 --max-time 60 "$archive/" |
    grep -oE 'href="v[0-9]+\.[0-9]+(\.[0-9]+)?/"' |
    sed -e 's|href="v||' -e 's|/"$||' |
    sort -V | tail -1)" || latest=""

printf '%s\t%s\t%s\t%s\t%s\t%s\t%s\n' e2fsprogs "$pinned" "${latest:-?}" - - - \
    "newest release on kernel.org; built static against musl"

[[ "${1:-}" == "--bump" ]] || exit 0

[[ -n "$latest" ]] || {
    echo "latest.sh: the release directory did not answer" >&2
    exit 1
}
[[ "$latest" != "$pinned" ]] || {
    echo "e2fsprogs $pinned is current"
    exit 0
}

echo "$latest" >"$here/VERSION"
echo "e2fsprogs: $pinned -> $latest"
echo "kernel.org's sha256sums file stands behind these bytes"
echo "next: make -C .. e2fsprogs"
