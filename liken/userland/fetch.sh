#!/usr/bin/env bash
#
# Download buildroot, the build system of the static programs, at the
# commit that userland/VERSION names.
#
# buildroot builds the programs, their musl cross-compiler, and every
# library they link from source tarballs, and its .hash files pin each
# tarball. So this pin covers every byte of the build except two: the
# builder image, which toolchains/images.toml pins by digest, and the
# SPDX schema below. build.sh explains the build.
#
# The pin is a commit on buildroot's master branch, not a release.
# buildroot releases every three months and then takes only fixes, so
# master is where a new upstream release of a program arrives first.
# GitHub serves the commit as a tarball that `git archive` writes, and
# nothing upstream signs or checksums it, so latest.sh --bump computes
# the digest from its own download.
#
# utils/generate-cyclonedx writes the CycloneDX SBOM. It reads the SPDX
# license list of the CycloneDX schema version that the script names,
# and it downloads that file when the download directory has none. This
# script puts the pinned file there first, so the SBOM step reads no
# unpinned byte.
#
# Usage:
#   userland/fetch.sh                  download and verify buildroot
#                                      and the SPDX schema
#   userland/fetch.sh --sources-only   download and verify buildroot
#                                      alone, for the source mirror
#
# The tarball lands in userland/cache/buildroot/, and the schema in
# userland/cache/dl/cyclonedx/, the download directory that build.sh
# gives buildroot.

set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source-path=SCRIPTDIR source=../../toolchains/pin.sh
source "$here/../../toolchains/pin.sh"

require_tools sha256sum tar

commit="$(cat "$here/VERSION")"

declare -A buildroot_sha256=(
    ["ccdd1ca4aac11d913f10eb071d7c15d5fbede9a4"]="336913f25e7d307b596d812ebd4a806c7813bee32c71effa24319a9bd4f75635"
)
tarball="$here/cache/buildroot/buildroot-$commit.tar.gz"
fetch_verified "https://github.com/buildroot/buildroot/archive/$commit.tar.gz" \
    "${buildroot_sha256[$commit]:-}" "$tarball"

[[ "${1:-}" != "--sources-only" ]] || exit 0

# The schema version is the default that the pinned generate-cyclonedx
# names, so a buildroot bump that moves it needs a new line here, and
# latest.sh --bump writes it.
declare -A spdx_schema_sha256=(
    ["1.6.2"]="c41917196639055e9f9670811bac23ef777732144f3ff5a2f39686f61580dbe6"
)
schema_version="$(tar -xzOf "$tarball" "buildroot-$commit/utils/generate-cyclonedx" |
    sed -n 's/^DEFAULT_CYCLONEDX_VERSION = "\(.*\)"$/\1/p')"
[[ -n "$schema_version" ]] || fail "utils/generate-cyclonedx names no DEFAULT_CYCLONEDX_VERSION"
fetch_verified "https://raw.githubusercontent.com/CycloneDX/specification/$schema_version/schema/spdx.schema.json" \
    "${spdx_schema_sha256[$schema_version]:-}" "$here/cache/dl/cyclonedx/spdx-$schema_version.schema.json"
