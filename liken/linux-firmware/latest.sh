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
# kernel.org's checksum autosigner clearsigns each sums file, and
# --bump copies a digest only from a sums file whose signature
# verifies against the autosigner's key.
# trust/keys/kernel-org-digest.sh makes the check, and
# trust/keys/README.md says where the key comes from and what the
# signature proves.
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
# shellcheck source-path=SCRIPTDIR source=../../toolchains/pin.sh
source "$here/../../toolchains/pin.sh"

archive="https://cdn.kernel.org/pub/linux/kernel/firmware"
regdb_archive="https://mirrors.edge.kernel.org/pub/software/network/wireless-regdb"

pinned="$(cat "$here/VERSION")"
# The regulatory database is a nested pin: its entry in pin.toml holds
# the version and the digest, because this domain's VERSION file names
# the firmware release and nothing else.
regdb_pinned="$(pin_value wireless-regdb version)"

latest="$(fetch_text "$archive/" |
    grep -oE 'linux-firmware-[0-9]{8}\.tar\.xz' |
    sed -e 's|^linux-firmware-||' -e 's|\.tar\.xz$||' |
    sort | tail -1)" || latest=""
regdb_latest="$(fetch_text "$regdb_archive/" |
    grep -oE 'wireless-regdb-[0-9]{4}\.[0-9]{2}\.[0-9]{2}\.tar\.xz' |
    sed -e 's|^wireless-regdb-||' -e 's|\.tar\.xz$||' |
    sort | tail -1)" || regdb_latest=""

row linux-firmware "$pinned" "$latest" - - - "newest release tarball on kernel.org"
row '  wireless-regdb' "$regdb_pinned" "$regdb_latest" - - - \
    "regulatory.db, which cfg80211 declares"

[[ "${1:-}" == "--bump" ]] || exit 0

# sums_digest <directory URL> <filename>: print the digest that the
# directory's signed sums file lists for one file.
sums_digest() {
    "$here/../../trust/keys/kernel-org-digest.sh" "$1/sha256sums.asc" "$2"
}

if [[ "${2:-linux-firmware}" == "wireless-regdb" ]]; then
    bump_needed wireless-regdb "$regdb_pinned" "$regdb_latest" || exit 0
    regdb_tarball="wireless-regdb-$regdb_latest.tar.xz"
    regdb_digest="$(sums_digest "$regdb_archive" "$regdb_tarball")"
    regdb_url="$regdb_archive/$regdb_tarball"
    # licensing/sources.sh mirrors this tarball by the url and the
    # digest of this entry, so the bump moves the mirror too.
    set_pin wireless-regdb "$regdb_latest" "$regdb_url" "$regdb_digest" \
        "$(generic_purl wireless-regdb "$regdb_latest" "$regdb_url" "$regdb_digest")"
    bumped wireless-regdb "$regdb_pinned" "$regdb_latest"
    echo "the digest came from sha256sums.asc, signed by kernel.org's checksum autosigner"
    next_step "make -C .. linux-firmware"
    exit 0
fi

[[ "${2:-linux-firmware}" == "linux-firmware" ]] || fail "no pin named ${2}; try wireless-regdb"

bump_needed linux-firmware "$pinned" "$latest" || exit 0

tarball="linux-firmware-$latest.tar.xz"
digest="$(sums_digest "$archive" "$tarball")"

echo "$latest" >"$here/VERSION"
set_pin linux-firmware "$latest" "$archive/$tarball" "$digest" \
    "$(generic_purl linux-firmware "$latest" "$archive/$tarball" "$digest")"
bumped linux-firmware "$pinned" "$latest"
echo "the digest came from sha256sums.asc, signed by kernel.org's checksum autosigner"
echo "the microcode domain builds AMD's blobs from this tarball; rebuild it too"
# licensing/sources.sh needs no re-pin after this bump. It mirrors the
# firmware tarball from this domain's own cache, which fetch.sh
# verified against the digest in pin.toml, so sources.sh holds no
# digest of its own for it.
next_step "make -C .. linux-firmware microcode"
