#!/usr/bin/env bash
#
# Report what this domain pins, and what the e2fsprogs maintainer has
# released now.
#
# The pin is an e2fsprogs release, and the bytes are its source
# tarball on kernel.org, which e2fsprogs/fetch.sh builds into a static
# mke2fs. `liken` builds this domain from source, so the pin has no
# channel, and both channel columns print `-`. Under the rule in root
# plan 83 it follows the newest e2fsprogs release. The image that the
# build runs in is the `alpine` entry in toolchains/images.toml, and
# toolchains/latest.sh reports it.
#
# kernel.org keeps one directory for each release, named v<version>,
# so the directory listing is the list of releases. Release candidates
# go into the testing/ directory beside them, so the listing at the
# top holds releases only.
#
# The pin carries the tarball's digest in fetch.sh. Each release
# directory holds a sha256sums.asc file, and --bump copies the
# tarball's line from it into fetch.sh.
#
# kernel.org's checksum autosigner clearsigns each sums file. It is a
# system off the network that hashes every upload to kernel.org's
# central storage and signs the result with a key that exists only
# there, as https://www.kernel.org/signature.html describes. Its
# public key is in autosigner.asc, taken from kernel.org's pgpkeys
# repository (keys/632D3A06589DA6B1.asc in
# https://git.kernel.org/pub/scm/docs/kernel/pgpkeys.git), and
# kernel.org's web key directory serves the same key for
# autosigner@kernel.org. --bump copies a digest only from a sums file
# whose signature verifies against that key, and its fingerprint is
# pinned below as `signer`. The signature proves that the tarball is
# the one on kernel.org's central storage. It does not replace the
# maintainer's own signature of the tarball, which nothing here checks.
#
# Usage:
#   e2fsprogs/latest.sh          report the pin and the newest release
#   e2fsprogs/latest.sh --bump   write the newest release into VERSION,
#                                and its digest into fetch.sh

set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

archive="https://www.kernel.org/pub/linux/kernel/people/tytso/e2fsprogs"

pinned="$(cat "$here/VERSION")"

latest="$(curl -fsS --retry 3 --max-time 60 "$archive/" |
    grep -oE 'href="v[0-9]+\.[0-9]+(\.[0-9]+)?/"' |
    sed -e 's|href="v||' -e 's|/"$||' |
    sort -V | tail -1)" || latest=""

printf '%s\t%s\t%s\t%s\t%s\t%s\t%s\n' e2fsprogs "$pinned" "${latest:-?}" - - - \
    "newest release on kernel.org; built static against musl"

[[ "${1:-}" == "--bump" ]] || exit 0

[[ -n "$latest" ]] || {
    echo "latest.sh: the release directory did not answer" >&2
    exit 1
}
[[ "$latest" != "$pinned" ]] || {
    echo "e2fsprogs $pinned is current"
    exit 0
}

for tool in curl gpg gpgv; do
    command -v "$tool" >/dev/null || {
        echo "latest.sh: missing required tool: $tool" >&2
        exit 1
    }
done

signer="B8868C80BA62A1FFFAF5FDA9632D3A06589DA6B1"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

sums="$archive/v$latest/sha256sums.asc"
curl -fsSL --retry 3 --max-time 60 -o "$work/sha256sums.asc" "$sums" || {
    echo "latest.sh: $sums did not answer" >&2
    exit 1
}
# The sums file is a clearsigned document. gpgv writes out the signed
# text alone, so the digest comes only from lines the signature
# covers, and never from text that someone added around the signed
# block. gpgv reads a binary keyring, and the repository keeps the key
# in its armored form, which a person can read and compare. gpgv's
# status lines name the fingerprint of the key that made a good
# signature, and the bump accepts only the pinned one. A sums file
# with no signature fails here too.
gpg --dearmor <"$here/autosigner.asc" >"$work/autosigner.gpg"
gpgv --status-fd 3 --keyring "$work/autosigner.gpg" --output "$work/signed" \
    "$work/sha256sums.asc" 3>"$work/status" || {
    echo "latest.sh: $sums does not verify against autosigner.asc" >&2
    exit 1
}
awk -v f="$signer" '$2 == "VALIDSIG" && $NF == f { found = 1 } END { exit !found }' "$work/status" || {
    echo "latest.sh: $sums is not signed by $signer" >&2
    exit 1
}

# Matching the exact filename picks the one line that matters.
digest="$(awk -v t="e2fsprogs-$latest.tar.gz" '$2 == t { print $1 }' "$work/signed")" || digest=""
[[ "$digest" =~ ^[0-9a-f]{64}$ ]] || {
    echo "latest.sh: $sums lists no e2fsprogs-$latest.tar.gz" >&2
    exit 1
}

# The array in fetch.sh holds one entry, the pinned version's, so the
# bump replaces the line after the array's opening line.
echo "$latest" >"$here/VERSION"
sed -i "/^declare -A e2fsprogs_sha256=(/{n;s|.*|    [\"$latest\"]=\"$digest\"|}" "$here/fetch.sh"
echo "e2fsprogs: $pinned -> $latest"
echo "the digest came from sha256sums.asc, signed by kernel.org's checksum autosigner"
echo "next: make -C .. e2fsprogs"
