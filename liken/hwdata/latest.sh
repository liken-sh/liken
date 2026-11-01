#!/usr/bin/env bash
#
# Report what this domain pins, and what the hwdata project has
# tagged now.
#
# The pin is a git tag, because that is what the fetch names: the
# project publishes no release archive for this file, so fetch.sh
# reads pci.ids straight out of the tagged tree. The tag list comes
# from git for the same reason it does in the other domains here: no
# token, no rate limit.
#
# `liken` takes the file as the hwdata project tags it, and no package
# repository stands between, so the pin has no channel and both
# channel columns print `-`. Under the rule in root plan 83 it follows
# the newest tag of the project.
#
# Nothing upstream signs or checksums this file, so a bump computes
# the digest from its own download. That pins the bytes for every
# later rebuild. It proves nothing about where they came from, which
# is why the bump says so.
#
# Usage:
#   hwdata/latest.sh          report the pin and the newest tag
#   hwdata/latest.sh --bump   write the newest tag and its digest

set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source-path=SCRIPTDIR source=../../toolchains/pin.sh
source "$here/../../toolchains/pin.sh"

repository="https://github.com/vcrhonek/hwdata"

pinned="$(cat "$here/VERSION")"

latest="$(newest_tag "$repository" '^v[0-9]+\.[0-9]+$')" || latest=""

row hwdata "$pinned" "$latest" - - - "newest tag; the file ships from the tagged tree"

[[ "${1:-}" == "--bump" ]] || exit 0

bump_needed hwdata "$pinned" "$latest" || exit 0

url="https://raw.githubusercontent.com/vcrhonek/hwdata/$latest/pci.ids"
digest="$(digest_of_url "$url")"

echo "$latest" >"$here/VERSION"
set_pin hwdata "$latest" "$url" "$digest" "$(github_purl vcrhonek/hwdata "$latest")"
bumped hwdata "$pinned" "$latest"
echo "the digest came from this download; nothing upstream signs it"
next_step "make -C .. hwdata"
