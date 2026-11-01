#!/usr/bin/env bash
#
# Report the release of the CycloneDX specification whose JSON schema
# this directory holds, and the newest release that the specification
# has now.
#
# releases/sbom checks every SBOM that it writes against this schema
# before it writes the file, and its tests check their output against
# it too. The tests run with no network, so the schema is in the
# repository: the bytes in this directory are the pin, SHA256SUMS
# holds the digest of each file, and a review of a bump reads every
# changed line. bom-<minor>.schema.json is the BOM
# schema, and it names the other three files by a relative $ref. The
# minor version in its name is the specVersion that the SBOM states.
#
# The schema comes from the tagged tree of CycloneDX/specification,
# with no package repository between, so the pin has no channel. Under
# the rule in root plan 83 it follows the newest release. Nothing
# upstream signs the files, so a bump takes the bytes that the tag
# serves, and the diff of the bump shows them.
#
# Usage:
#   releases/sbom/schema/latest.sh          report the pin and the newest release
#   releases/sbom/schema/latest.sh --bump   write the schema of the newest release

set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source-path=SCRIPTDIR source=../../../../toolchains/pin.sh
source "$here/../../../../toolchains/pin.sh"

pinned="$(cat "$here/VERSION")"

latest="$(github_api repos/CycloneDX/specification/releases/latest | jq -r .tag_name)" || latest=""

row cyclonedx-schema "$pinned" "$latest" - - - "the newest release of CycloneDX/specification"

[[ "${1:-}" == "--bump" ]] || exit 0

bump_needed cyclonedx-schema "$pinned" "$latest" || exit 0

# A new minor version renames the BOM schema, so the old file goes and
# the new one takes its place. releases/sbom reads the one file that
# matches bom-*.schema.json.
minor="$(cut -d. -f1-2 <<<"$latest")"
base="https://raw.githubusercontent.com/CycloneDX/specification/$latest/schema"
rm -f "$here"/bom-*.schema.json
for file in "bom-$minor.schema.json" spdx.schema.json jsf-0.82.schema.json cryptography-defs.schema.json; do
    fetch_file "$base/$file" "$here/$file"
done
# The digests record the bytes that the tag served, and a test of
# releases/sbom fails when a file no longer matches its digest.
(cd "$here" && sha256sum -- *.schema.json >SHA256SUMS)

echo "$latest" >"$here/VERSION"
bumped cyclonedx-schema "$pinned" "$latest"
echo "the schema came from the tag; nothing upstream signs it"
next_step "make -C liken test-go, and read releases/sbom for what the new specVersion changes"
