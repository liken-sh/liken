#!/usr/bin/env bash
#
# Report what this domain pins, and what kernel.org has published now.
#
# The firmware project cuts a release every few weeks and names it for
# the date. There is no tag list to read here, because the fetch takes
# the release tarball from kernel.org, and that directory listing is
# the index of every release.
#
# kernel.org publishes one sha256sums file for the whole directory, so
# a bump reads the new digest from upstream rather than from its own
# download. That matters more here than anywhere else in the
# repository: this tarball is the largest thing liken fetches, and a
# bump that had to hash it would download it twice.
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
# pinned below as `signer`.
#
# `liken` takes each tarball as its project releases it, and no
# package repository stands between, so the pins have no channel and
# the channel columns print `-`. Under the rule in root plan 83 each
# pin follows the newest release of its project.
#
# The second pin, wireless-regdb, publishes the same way, in the same
# archive, with its own sums file that the same autosigner signs. The
# two releases are cut on separate schedules, so each one bumps on its
# own.
#
# Usage:
#   linux-firmware/latest.sh          report both pins
#   linux-firmware/latest.sh --bump   write the newest firmware release
#                                     and its digest
#   linux-firmware/latest.sh --bump wireless-regdb
#                                     write the newest regulatory
#                                     database release and its digest

set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

archive="https://cdn.kernel.org/pub/linux/kernel/firmware"
regdb_archive="https://mirrors.edge.kernel.org/pub/software/network/wireless-regdb"

pinned="$(cat "$here/VERSION")"
# The regulatory database is a nested pin: fetch.sh holds the version
# and the digest, because this domain's VERSION file names the
# firmware release and nothing else.
regdb_pinned="$(sed -n 's|^regdb_version="\(.*\)"$|\1|p' "$here/fetch.sh")"

latest="$(curl -fsS --retry 3 --max-time 60 "$archive/" |
    grep -oE 'linux-firmware-[0-9]{8}\.tar\.xz' |
    sed -e 's|^linux-firmware-||' -e 's|\.tar\.xz$||' |
    sort | tail -1)" || latest=""
regdb_latest="$(curl -fsS --retry 3 --max-time 60 "$regdb_archive/" |
    grep -oE 'wireless-regdb-[0-9]{4}\.[0-9]{2}\.[0-9]{2}\.tar\.xz' |
    sed -e 's|^wireless-regdb-||' -e 's|\.tar\.xz$||' |
    sort | tail -1)" || regdb_latest=""

printf '%s\t%s\t%s\t%s\t%s\t%s\t%s\n' linux-firmware "$pinned" "${latest:-?}" - - - \
    "newest release tarball on kernel.org"
printf '%s\t%s\t%s\t%s\t%s\t%s\t%s\n' '  wireless-regdb' "$regdb_pinned" \
    "${regdb_latest:-?}" - - - "regulatory.db, which cfg80211 declares"

[[ "${1:-}" == "--bump" ]] || exit 0

for tool in curl gpg gpgv; do
    command -v "$tool" >/dev/null || {
        echo "latest.sh: missing required tool: $tool" >&2
        exit 1
    }
done

signer="B8868C80BA62A1FFFAF5FDA9632D3A06589DA6B1"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT
# gpgv reads a binary keyring, and the repository keeps the key in its
# armored form, which a person can read and compare.
gpg --dearmor <"$here/autosigner.asc" >"$work/autosigner.gpg"

# sums_digest <directory URL> <filename>: print the digest that the
# directory's signed sums file lists for one file. The sums file is a
# clearsigned document. gpgv writes out the signed text alone, so the
# digest comes only from lines the signature covers, and never from
# text that someone added around the signed block. gpgv's status lines
# name the fingerprint of the key that made a good signature, and only
# the pinned one is accepted. A sums file with no signature fails too.
# This function runs in a command substitution, where set -e does not
# reach, so each failure exits on its own.
sums_digest() {
    local sums="$1/sha256sums.asc"
    curl -fsSL --retry 3 --max-time 60 -o "$work/sha256sums.asc" "$sums" || {
        echo "latest.sh: $sums did not answer" >&2
        exit 1
    }
    gpgv --status-fd 3 --keyring "$work/autosigner.gpg" --output "$work/signed" \
        "$work/sha256sums.asc" 3>"$work/status" >&2 || {
        echo "latest.sh: $sums does not verify against autosigner.asc" >&2
        exit 1
    }
    awk -v f="$signer" '$2 == "VALIDSIG" && $NF == f { found = 1 } END { exit !found }' "$work/status" || {
        echo "latest.sh: $sums is not signed by $signer" >&2
        exit 1
    }
    awk -v t="$2" '$2 == t { print $1 }' "$work/signed"
}

if [[ "${2:-linux-firmware}" == "wireless-regdb" ]]; then
    [[ -n "$regdb_latest" ]] || {
        echo "latest.sh: the wireless-regdb directory did not answer" >&2
        exit 1
    }
    [[ "$regdb_latest" != "$regdb_pinned" ]] || {
        echo "wireless-regdb $regdb_pinned is current"
        exit 0
    }
    regdb_tarball="wireless-regdb-$regdb_latest.tar.xz"
    regdb_digest="$(sums_digest "$regdb_archive" "$regdb_tarball")"
    [[ -n "$regdb_digest" ]] || {
        echo "latest.sh: no $regdb_tarball listed in $regdb_archive/sha256sums.asc" >&2
        exit 1
    }
    sed -i \
        -e "s|^regdb_version=\".*\"|regdb_version=\"$regdb_latest\"|" \
        -e "s|^regdb_digest=\".*\"|regdb_digest=\"$regdb_digest\"|" \
        "$here/fetch.sh"
    echo "wireless-regdb: $regdb_pinned -> $regdb_latest"
    echo "the digest came from sha256sums.asc, signed by kernel.org's checksum autosigner"
    # licensing/sources.sh mirrors this tarball and pins it by digest,
    # so a regdb bump moves that digest too. A refused re-pin leaves
    # the domain pin moved and the mirror behind it, which is a
    # release that cannot serve its own sources. Say that plainly,
    # because the fix is a hand edit.
    "$here/../licensing/sources.sh" --repin || {
        echo "the pin moved; licensing/sources.sh still needs a hand edit" >&2
        exit 1
    }
    echo "next: make -C .. linux-firmware"
    exit 0
fi

[[ "${2:-linux-firmware}" == "linux-firmware" ]] || {
    echo "latest.sh: no pin named ${2}; try wireless-regdb" >&2
    exit 1
}

[[ -n "$latest" ]] || {
    echo "latest.sh: the firmware directory did not answer" >&2
    exit 1
}
[[ "$latest" != "$pinned" ]] || {
    echo "linux-firmware $pinned is current"
    exit 0
}

tarball="linux-firmware-$latest.tar.xz"
digest="$(sums_digest "$archive" "$tarball")"
[[ -n "$digest" ]] || {
    echo "latest.sh: no $tarball listed in $archive/sha256sums.asc" >&2
    exit 1
}

echo "$latest" >"$here/VERSION"
sed -i "s|^digest=\".*\"|digest=\"$digest\"|" "$here/fetch.sh"
echo "linux-firmware: $pinned -> $latest"
echo "the digest came from sha256sums.asc, signed by kernel.org's checksum autosigner"
echo "the microcode domain builds AMD's blobs from this tarball; rebuild it too"
# licensing/sources.sh needs no re-pin after this bump. It mirrors the
# firmware tarball from this domain's own cache, which fetch.sh
# verified against its own digest, so sources.sh holds no digest of
# its own for it.
echo "next: make -C .. linux-firmware microcode"
