#!/usr/bin/env bash
#
# Report the Sigstore trusted root that this directory pins, and the
# trusted root that Sigstore's TUF repository names now.
#
# `liken verify` and `liken sbom` check each signature against the
# keys in trusted_root.json: Fulcio's certificate authority, Rekor's
# logs, the certificate transparency logs, and the timestamp
# authority. The file is compiled into the `liken` toolkit, so a check
# reads no key from the network, and a review of a bump reads every
# key that changed. pin.toml holds the file's digest, the release's
# SBOM names it from there, and a test of verify/ fails when the file
# no longer matches the digest.
#
# Sigstore publishes the trusted root as a target of its TUF
# repository at tuf-repo-cdn.sigstore.dev, and the root has no version
# of its own. The pin's version is the version of the TUF targets
# metadata that named it. The report reads the current targets
# metadata without checking its signatures, because the report only
# compares a digest: it prints the current targets version when that
# version names a different trusted root, and the pinned version when
# the root is the same. No package repository stands between, so the
# pin has no channel, and under the rule in root plan 83 it follows
# the newest root.
#
# --bump takes the root through TUF, with the cosign of
# toolchains/images.toml. `cosign initialize` starts from the TUF root
# metadata compiled into cosign, follows each signed rotation of it,
# checks the signatures and the expiry of the timestamp, snapshot, and
# targets metadata, and downloads trusted_root.json only when its
# digest matches what the signed targets metadata names. So the bump
# trusts the same chain that cosign itself trusts, and a mirror or a
# CDN that serves a changed file fails the bump. cosign runs from its
# image, so --bump needs docker.
#
# Usage:
#   verify/latest.sh          report the pin and the root that TUF names now
#   verify/latest.sh --bump   take the current root through TUF and pin it

set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source-path=SCRIPTDIR source=../../toolchains/pin.sh
source "$here/../../toolchains/pin.sh"

require_tools jq

tuf="https://tuf-repo-cdn.sigstore.dev"
entry=sigstore-trusted-root

pinned="$(cat "$here/VERSION")"
pinned_digest="$(pin_value "$entry" sha256)"

# The repository writes consistent snapshots: timestamp.json names the
# version of the snapshot, the snapshot names the version of the
# targets metadata, and each one is at <version>.<name>.json.
latest=""
if snapshot="$(fetch_text "$tuf/timestamp.json" | jq -er '.signed.meta["snapshot.json"].version')" &&
    targets="$(fetch_text "$tuf/$snapshot.snapshot.json" | jq -er '.signed.meta["targets.json"].version')" &&
    digest="$(fetch_text "$tuf/$targets.targets.json" | jq -er '.signed.targets["trusted_root.json"].hashes.sha256')"; then
    if [[ "$digest" == "$pinned_digest" ]]; then
        latest="$pinned"
    else
        latest="$targets"
    fi
fi

row "$entry" "$pinned" "$latest" - - - "the trusted root that Sigstore's TUF targets name"

[[ "${1:-}" == "--bump" ]] || exit 0

bump_needed "$entry" "$pinned" "$latest" || exit 0

require_tools docker
cosign="$(image_of cosign)"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT
docker run --rm --user "$(id -u):$(id -g)" -e HOME=/home \
    -v "$work:/home" "$cosign" initialize >&2 ||
    fail "cosign initialize did not complete the TUF update"

# cosign's TUF client keeps the metadata that it checked and the
# targets that it downloaded in its cache, under the repository's name.
cache="$work/.sigstore/root/tuf-repo-cdn.sigstore.dev"
version="$(jq -er '.signed.version' "$cache/targets.json")"
digest="$(jq -er '.signed.targets["trusted_root.json"].hashes.sha256' "$cache/targets.json")"
[[ "$(sha256sum <"$cache/targets/trusted_root.json" | cut -d' ' -f1)" == "$digest" ]] ||
    fail "the trusted root in cosign's cache does not match the digest that its targets metadata names"

cp "$cache/targets/trusted_root.json" "$here/trusted_root.json"
echo "$version" >"$here/VERSION"
url="$tuf/targets/$digest.trusted_root.json"
set_pin "$entry" "$version" "$url" "$digest" "$(generic_purl "$entry" "$version" "$url" "$digest")"
bumped "$entry" "$pinned" "$version"
echo "the root came through TUF, from the root metadata compiled into $cosign"
next_step "make -C liken test-go, and read the diff of trusted_root.json for each key that came or went"
