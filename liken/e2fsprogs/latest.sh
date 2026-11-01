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
# tarball's line from it into fetch.sh.
#
# kernel.org's checksum autosigner clearsigns each sums file, and
# --bump copies a digest only from a sums file whose signature
# verifies against the autosigner's key.
# trust/keys/kernel-org-digest.sh makes the check, and
# trust/keys/README.md says where the key comes from and what the
# signature proves.
#
# Usage:
#   e2fsprogs/latest.sh          report the pin and the newest release
#   e2fsprogs/latest.sh --bump   write the newest release into VERSION,
#                                and its digest into fetch.sh

set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source-path=SCRIPTDIR source=../../toolchains/pin.sh
source "$here/../../toolchains/pin.sh"

archive="https://www.kernel.org/pub/linux/kernel/people/tytso/e2fsprogs"

pinned="$(cat "$here/VERSION")"

latest="$(fetch_text "$archive/" |
    grep -oE 'href="v[0-9]+\.[0-9]+(\.[0-9]+)?/"' |
    sed -e 's|href="v||' -e 's|/"$||' |
    sort -V | tail -1)" || latest=""

row e2fsprogs "$pinned" "$latest" - - - \
    "newest release on kernel.org; built static against musl"

[[ "${1:-}" == "--bump" ]] || exit 0

bump_needed e2fsprogs "$pinned" "$latest" || exit 0

sums="$archive/v$latest/sha256sums.asc"
digest="$("$here/../../trust/keys/kernel-org-digest.sh" "$sums" "e2fsprogs-$latest.tar.gz")"

echo "$latest" >"$here/VERSION"
set_entry "$here/fetch.sh" e2fsprogs_sha256 "$latest" "$digest"
bumped e2fsprogs "$pinned" "$latest"
echo "the digest came from sha256sums.asc, signed by kernel.org's checksum autosigner"
next_step "make -C .. e2fsprogs"
