#!/usr/bin/env bash
#
# Report what this domain pins, and what Intel has released now.
#
# Only the Intel half of this domain has a pin of its own. The AMD
# blobs come out of the linux-firmware tarball, and fetch.sh reads
# linux-firmware/VERSION directly, so that half cannot drift from the
# domain that owns it.
#
# Intel names each release for the date it was cut, and reissues one
# as a -rev tag when a build has to be replaced. The version sort
# keeps those in order behind their date, so the newest tag wins
# either way.
#
# `liken` takes Intel's data files as Intel releases them, and no
# package repository stands between, so the pin has no channel and
# both channel columns print `-`. Under the rule in root plan 83 it
# follows Intel's newest release.
#
# Nothing upstream checksums the GitHub source archive, so a bump
# computes the digest from its own download.
#
# Usage:
#   microcode/latest.sh          report the pin and the newest release
#   microcode/latest.sh --bump   write the newest release and its digest

set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source-path=SCRIPTDIR source=../../toolchains/pin.sh
source "$here/../../toolchains/pin.sh"

repository="https://github.com/intel/Intel-Linux-Processor-Microcode-Data-Files"

pinned="$(cat "$here/VERSION")"

latest="$(newest_tag "$repository" '^[0-9]{8}' microcode-)" || latest=""

row microcode "$pinned" "$latest" - - - \
    "Intel's data files; the AMD blobs follow linux-firmware"

[[ "${1:-}" == "--bump" ]] || exit 0

bump_needed microcode "$pinned" "$latest" || exit 0

digest="$(digest_of_url "$repository/archive/refs/tags/microcode-$latest.tar.gz")"

echo "$latest" >"$here/VERSION"
set_var "$here/fetch.sh" digest "$digest"
bumped microcode "$pinned" "$latest"
echo "the digest came from this download; nothing upstream signs it"
next_step "make -C .. microcode"
