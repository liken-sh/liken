#!/usr/bin/env bash
#
# Report the cargo-auditable that fetch.sh pins, and the newest release
# of the upstream project.
#
# liken takes the binary that the project builds and attaches to each
# GitHub release, and no package repository stands between, so the pin
# has no channel and both channel columns print `-`. Under the rule in
# root plan 83, it follows the newest release tag.
#
# --bump reads the `.sha256` file that the release publishes beside the
# archive and writes the version and the digest into fetch.sh. The
# project signs neither file and publishes no attestation, so the
# digest rests on the HTTPS connection to GitHub alone.
#
# Usage:
#   cargo-auditable/latest.sh          report the pin and the newest release
#   cargo-auditable/latest.sh --bump   write the newest release into fetch.sh

set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source-path=SCRIPTDIR source=../pin.sh
source "$here/../pin.sh"

pinned="$(var_of "$here/fetch.sh" version)"
latest="$(newest_tag https://github.com/rust-secure-code/cargo-auditable '^[0-9]+\.[0-9]+\.[0-9]+$' v)" || latest=""

row cargo-auditable "$pinned" "$latest" - - - "newest release tag"

[[ "${1:-}" == "--bump" ]] || exit 0

bump_needed cargo-auditable "$pinned" "$latest" || exit 0

asset="cargo-auditable-x86_64-unknown-linux-gnu.tar.xz"
digest="$(fetch_text "https://github.com/rust-secure-code/cargo-auditable/releases/download/v$latest/$asset.sha256" |
    cut -d' ' -f1)" || digest=""
[[ "$digest" =~ ^[0-9a-f]{64}$ ]] || fail "release v$latest publishes no sha256 for $asset"
set_var "$here/fetch.sh" version "$latest"
set_var "$here/fetch.sh" sha256 "$digest"
bumped cargo-auditable "$pinned" "$latest"
echo "the release's .sha256 file stands behind these bytes; the project signs nothing"
next_step "make workflows at the top of the repository, then build each image that"
echo "builds a Rust binary: grep -l 'cargo auditable' in the Dockerfiles"
