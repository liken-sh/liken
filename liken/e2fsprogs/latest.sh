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
# The pin carries the tarball's digest in fetch.sh. Each release
# directory holds a sha256sums.asc file, and --bump copies the
# tarball's line from it into fetch.sh. The file is clearsigned by the
# maintainer, and nothing here checks that signature.
#
# Usage:
#   e2fsprogs/latest.sh          report the pin and the newest release
#   e2fsprogs/latest.sh --bump   write the newest release into VERSION,
#                                and its digest into fetch.sh

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

# The sums file is a clearsigned document, so the digest lines sit
# among the signature's own text. Matching the exact filename picks
# the one line that matters.
sums="$archive/v$latest/sha256sums.asc"
digest="$(curl -fsSL --retry 3 --max-time 60 "$sums" |
    awk -v t="e2fsprogs-$latest.tar.gz" '$2 == t { print $1 }')" || digest=""
[[ "$digest" =~ ^[0-9a-f]{64}$ ]] || {
    echo "latest.sh: $sums lists no e2fsprogs-$latest.tar.gz" >&2
    exit 1
}

# The array in fetch.sh holds one entry, the pinned version's, so the
# bump replaces the line after the array's opening line.
echo "$latest" >"$here/VERSION"
sed -i "/^declare -A e2fsprogs_sha256=(/{n;s|.*|    [\"$latest\"]=\"$digest\"|}" "$here/fetch.sh"
echo "e2fsprogs: $pinned -> $latest"
echo "the digest came from sha256sums.asc; check its signature by hand"
echo "next: make -C .. e2fsprogs"
