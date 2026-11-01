#!/usr/bin/env bash
#
# Print the sha256 that a kernel.org sums file lists for one file,
# after the file's signature verifies against kernel.org's checksum
# autosigner key.
#
# A bump that copies a digest from a sha256sums.asc file on kernel.org
# calls this script, so that a sums file someone changed on a mirror
# or in transit stops the bump instead of becoming the pin. The key is
# kernel-org-autosigner.asc beside this script, and README.md beside
# it says where the key comes from.
#
# The sums file is a clearsigned document. gpgv writes out the signed
# text alone, so the digest comes only from lines the signature
# covers, and never from text that someone added around the signed
# block. The script accepts only the fingerprint pinned below as
# `signer`. A sums file with no signature fails too.
#
# Usage:
#   trust/keys/kernel-org-digest.sh <sha256sums.asc URL> <filename>
#
# The script prints the 64-character digest on stdout and exits 0, or
# prints the reason on stderr and exits 1.

set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source-path=SCRIPTDIR source=../../toolchains/pin.sh
source "$here/../../toolchains/pin.sh"

sums="$1"
file="$2"
signer="B8868C80BA62A1FFFAF5FDA9632D3A06589DA6B1"

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

fetch_file "$sums" "$work/sha256sums.asc"
verify_signature --signer "$signer" "$here/kernel-org-autosigner.asc" \
    "$work/sha256sums.asc" >"$work/signed"

# Matching the exact filename picks the one line that matters.
digest="$(awk -v t="$file" '$2 == t { print $1 }' "$work/signed")"
[[ "$digest" =~ ^[0-9a-f]{64}$ ]] || fail "$sums lists no $file"
echo "$digest"
