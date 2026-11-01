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
# `liken` takes the release binaries as the Flux project publishes
# them, and no package repository stands between, so the pin has no
# channel and both channel columns print `-`. Under the rule in root
# plan 83 it follows the newest release of Flux.
#
# The pin carries the CLI tarball's digest in flux/fetch.sh. Each Flux
# release publishes a checksums file, and --bump copies the tarball's
# line from it into fetch.sh.
#
# Flux signs the checksums file keylessly with sigstore: the release
# workflow gets a short-lived certificate from sigstore's Fulcio for
# its GitHub Actions identity, signs the file, records the signature
# in sigstore's Rekor log, and publishes the result beside the file as
# flux_<version>_checksums.txt.sigstore.json. --bump verifies that
# bundle with cosign before it reads a digest, and stops when the
# bundle is missing, does not verify, or names another signer. The
# signer must be the release workflow of fluxcd/flux2 at the tag of
# the release, as GitHub's OIDC issuer certified it, so a checksums
# file that a person or another workflow signed fails too. cosign runs
# from the image that the `cosign` entry of toolchains/images.toml
# pins by digest, so --bump needs docker.
#
# Usage:
#   flux/latest.sh          report the pin and the newest release
#   flux/latest.sh --bump   write the newest release into VERSION, and
#                           its digest into fetch.sh

set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source-path=SCRIPTDIR source=../../toolchains/pin.sh
source "$here/../../toolchains/pin.sh"

repository="https://github.com/fluxcd/flux2"

pinned="$(cat "$here/VERSION")"

latest="$(newest_tag "$repository" '^v[0-9]+\.[0-9]+\.[0-9]+$')" || latest=""

row flux "$pinned" "$latest" - - - "newest release tag, no release candidates"

[[ "${1:-}" == "--bump" ]] || exit 0

bump_needed flux "$pinned" "$latest" || exit 0

require_tools docker
cosign="$(image_of cosign)"
identity="$repository/.github/workflows/release.yaml@refs/tags/$latest"
issuer="https://token.actions.githubusercontent.com"

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

# Release artifacts name the version without the leading v.
bare="${latest#v}"
sums="$repository/releases/download/$latest/flux_${bare}_checksums.txt"
tarball="flux_${bare}_linux_amd64.tar.gz"
fetch_file "$sums" "$work/checksums.txt"
fetch_file "$sums.sigstore.json" "$work/checksums.txt.sigstore.json"
# The container runs as the calling user, so it reads the work
# directory without a change to its permissions, and HOME points at a
# writable directory for the sigstore trust root that cosign fetches
# and caches.
docker run --rm --user "$(id -u):$(id -g)" -e HOME=/tmp \
    -v "$work:/work:ro" "$cosign" verify-blob \
    --bundle /work/checksums.txt.sigstore.json \
    --certificate-identity "$identity" \
    --certificate-oidc-issuer "$issuer" \
    /work/checksums.txt >&2 || fail "$sums does not verify as signed by $identity"
digest="$(awk -v f="$tarball" '$2 == f { print $1 }' "$work/checksums.txt")"
[[ "$digest" =~ ^[0-9a-f]{64}$ ]] || fail "$sums lists no $tarball"

echo "$latest" >"$here/VERSION"
set_entry "$here/fetch.sh" flux_sha256 "$latest" "$digest"
bumped flux "$pinned" "$latest"
echo "the digest came from the release's checksums file, signed by flux's release workflow"
next_step "make -C .. cluster-operator"
