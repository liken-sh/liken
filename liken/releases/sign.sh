#!/usr/bin/env bash
# Sign each file of one OS release with cosign:
#
#   releases/sign.sh <version>          keyless, in the publish job
#   releases/sign.sh <version> <key>    with a local key, for a lab
#                                       rehearsal
#
# The OS's publish job in CI runs this from liken/, after `make
# release` built the bundle into releases/dist/<version>/, and before
# releases/publish.sh uploads the directory. A machine trusts a
# release through the digest that its Cluster pins, and needs no
# signature. A person who downloads a file from the channel has no
# Cluster, and the signature lets that person check that this
# repository's release workflow published the file.
#
# The files are release.yaml and every artifact that it names, the
# SBOM among them. Each signature goes into a Sigstore bundle,
# <file>.sigstore.json, beside its file: the signature, the
# certificate that Fulcio issued, and the entry that Rekor recorded.
# publish.sh uploads every file of the directory, so each bundle goes
# up beside its file.
#
# Keyless, cosign takes the job's OIDC token from the two
# ACTIONS_ID_TOKEN_REQUEST variables, which a job with id-token: write
# holds, and Fulcio certifies the workflow at its ref as the signer, as
# ci/sign.go does for each image. With a key, the signing config names
# no transparency log, so a rehearsal writes nothing to the public
# Rekor, and verify-blob needs --insecure-ignore-tlog. The
# releases skill gives the commands that verify both kinds.
#
# cosign runs from the image that toolchains/images.toml pins, through
# docker or podman, as the calling user, and HOME points at a writable
# directory for the sigstore trust root that cosign fetches.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source-path=SCRIPTDIR source=../../toolchains/pin.sh
source "$here/../../toolchains/pin.sh"

usage="usage: releases/sign.sh <version> [<key>]"
version="${1:?$usage}"
key_file="${2:-}"
dist="$here/dist/$version"
[[ -f "$dist/release.yaml" ]] || fail "$dist/release.yaml does not exist; make release VERSION=$version writes it"

runtime="$(container_runtime)"
[[ -n "$runtime" ]] || fail "needs docker or podman to run cosign"
cosign="$(image_of cosign)"

# The artifacts are the names in the document's artifacts list, which
# Bundle writes as "  - name: <file>" lines before the components.
mapfile -t files < <(awk '/^[a-z]/ { inside = ($0 == "artifacts:") }
    inside && $1 == "-" && $2 == "name:" { print $3 }' "$dist/release.yaml")
((${#files[@]} > 0)) || fail "$dist/release.yaml names no artifact"
files+=(release.yaml)

run=("$runtime" run --rm --user "$(id -u):$(id -g)" -e HOME=/tmp -v "$dist:/release" -w /release)
if [[ -z "$key_file" ]]; then
    run+=(-e ACTIONS_ID_TOKEN_REQUEST_URL -e ACTIONS_ID_TOKEN_REQUEST_TOKEN "$cosign" sign-blob --yes)
else
    [[ -f "$key_file" ]] || fail "$key_file does not exist"
    key_dir="$(mktemp -d)"
    trap 'rm -rf "$key_dir"' EXIT
    cp "$key_file" "$key_dir/cosign.key"
    "$runtime" run --rm --user "$(id -u):$(id -g)" -e HOME=/tmp -v "$key_dir:/key" "$cosign" \
        signing-config create --out /key/signing-config.json >/dev/null
    run+=(-e COSIGN_PASSWORD -v "$key_dir:/key:ro" "$cosign" sign-blob --yes
        --key /key/cosign.key --signing-config /key/signing-config.json)
fi

for file in "${files[@]}"; do
    [[ -f "$dist/$file" ]] || fail "$dist/$file does not exist, and release.yaml names it"
    # cosign reports each step on stderr, so its output shows only
    # when the signature fails.
    output="$("${run[@]}" --bundle "$file.sigstore.json" "$file" 2>&1)" ||
        fail "signing $file: $output"
    echo "signed $file: $file.sigstore.json"
done
