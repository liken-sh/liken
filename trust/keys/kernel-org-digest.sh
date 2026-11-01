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
# block. gpgv reads a binary keyring, and the repository keeps the key
# in its armored form, which a person can read and compare. gpgv's
# status lines name the fingerprint of the key that made a good
# signature, and the script accepts only the fingerprint pinned below
# as `signer`. A sums file with no signature fails too.
#
# Usage:
#   trust/keys/kernel-org-digest.sh <sha256sums.asc URL> <filename>
#
# The script prints the 64-character digest on stdout and exits 0, or
# prints the reason on stderr and exits 1.

set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
sums="$1"
file="$2"
signer="B8868C80BA62A1FFFAF5FDA9632D3A06589DA6B1"

for tool in curl gpg gpgv; do
    command -v "$tool" >/dev/null || {
        echo "kernel-org-digest.sh: missing required tool: $tool" >&2
        exit 1
    }
done

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

curl -fsSL --retry 3 --max-time 60 -o "$work/sha256sums.asc" "$sums" || {
    echo "kernel-org-digest.sh: $sums did not answer" >&2
    exit 1
}
gpg --dearmor <"$here/kernel-org-autosigner.asc" >"$work/autosigner.gpg"
gpgv --status-fd 3 --keyring "$work/autosigner.gpg" --output "$work/signed" \
    "$work/sha256sums.asc" 3>"$work/status" >&2 || {
    echo "kernel-org-digest.sh: $sums does not verify against kernel-org-autosigner.asc" >&2
    exit 1
}
awk -v f="$signer" '$2 == "VALIDSIG" && $NF == f { found = 1 } END { exit !found }' "$work/status" || {
    echo "kernel-org-digest.sh: $sums is not signed by $signer" >&2
    exit 1
}

# Matching the exact filename picks the one line that matters.
digest="$(awk -v t="$file" '$2 == t { print $1 }' "$work/signed")"
[[ "$digest" =~ ^[0-9a-f]{64}$ ]] || {
    echo "kernel-org-digest.sh: $sums lists no $file" >&2
    exit 1
}
echo "$digest"
