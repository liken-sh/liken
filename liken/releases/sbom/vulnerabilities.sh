#!/usr/bin/env bash
#
# Match the SBOM of one OS release against the vulnerability
# databases:
#
#   releases/sbom/vulnerabilities.sh <sbom> [<sarif>]
#
# The script prints a table of the findings. With a second argument it
# also writes them as SARIF, which CI uploads to code scanning: the
# OS's build job under the category grype/liken, for the SBOM of what
# a commit built, and the OS's publish job and the scheduled scan
# under grype/liken-release, for the SBOM of a release. A finding does
# not fail the script: the scan reports, and a person decides what to
# bump.
#
# Trivy scans each container image that CI publishes, and the OS
# publishes no image. Its kernel, bootloaders, firmware, and k3s come
# from upstream releases that no package database records, so the
# only names a scanner can match them by are the CPEs that each
# pin.toml entry states. grype matches by CPE against NVD, and Trivy
# does not, so this one scan runs grype. It also matches the Go
# modules and the buildroot packages that the SBOM lists.
#
# The scan differs from grype's defaults in three ways:
#
#   - It leaves out each component with the scope "excluded". The SBOM
#     lists the compiler, the host tools, and the mirrored sources of a
#     build with that scope, because the release ships none of them,
#     and grype does not read the scope.
#   - It keeps only a finding with a fixed version, as the Trivy scan
#     of each image does. NVD records many old kernel vulnerabilities
#     with a version range that has no upper limit, and each of those
#     matches every kernel.
#   - grype.yaml, beside this script, turns on CPE matching for the
#     Ubuntu packages and names each finding that is false for every
#     release.
#
# grype runs from the image that toolchains/images.toml pins, through
# docker or podman, as the calling user. It downloads its database on
# each run into GRYPE_DB_CACHE_DIR, or into a directory that the
# script deletes when the variable is not set.

set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source-path=SCRIPTDIR source=../../../toolchains/pin.sh
source "$here/../../../toolchains/pin.sh"

usage="usage: vulnerabilities.sh <sbom> [<sarif>]"
sbom="${1:?$usage}"
sarif="${2:-}"
[[ -f "$sbom" ]] || fail "$sbom does not exist"
require_tools jq

runtime="$(container_runtime)"
[[ -n "$runtime" ]] || fail "needs docker or podman to run grype"
grype="$(image_of grype)"

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT
db="${GRYPE_DB_CACHE_DIR:-$work/db}"
mkdir -p "$db" "$work/out"

jq '.components |= map(select(.scope != "excluded"))' "$sbom" >"$work/sbom.cdx.json"
echo "scanning $(jq '.components | length' "$work/sbom.cdx.json") of the $(jq '.components | length' "$sbom") components of $sbom"

outputs=(-o table)
[[ -n "$sarif" ]] && outputs+=(-o sarif=/out/grype.sarif)

"$runtime" run --rm --user "$(id -u):$(id -g)" -e HOME=/tmp --tmpfs /tmp:exec,mode=1777 \
    -e GRYPE_DB_CACHE_DIR=/db -e GRYPE_CHECK_FOR_APP_UPDATE=false \
    -v "$work/sbom.cdx.json:/sbom.cdx.json:ro" -v "$here/grype.yaml:/grype.yaml:ro" \
    -v "$db:/db" -v "$work/out:/out" "$grype" \
    sbom:/sbom.cdx.json -c /grype.yaml --only-fixed -q "${outputs[@]}"

# Code scanning lists each finding by a path, and SARIF states a path
# with no leading slash. grype gives the path of the program that
# holds a Go module, such as /bin/k3s, which becomes bin/k3s. It gives
# an empty path for a component that a manifest or buildroot names,
# because the SBOM states none, so such a finding takes the path
# sbom.cdx.json, the file that names the component.
if [[ -n "$sarif" ]]; then
    mkdir -p "$(dirname "$sarif")"
    jq '(.runs[].results[].locations[].physicalLocation.artifactLocation.uri) |=
        (ltrimstr("/") | if . == "" then "sbom.cdx.json" else . end)' \
        "$work/out/grype.sarif" >"$sarif"
    echo "wrote $sarif: $(jq '[.runs[].results[]] | length' "$sarif") findings"
fi
