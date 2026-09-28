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
# The second pin, wireless-regdb, publishes the same way, in the same
# archive, with its own clearsigned sums file. The two releases are
# cut on separate schedules, so each one bumps on its own.
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

latest="$(curl -fsS --retry 3 "$archive/" |
    grep -oE 'linux-firmware-[0-9]{8}\.tar\.xz' |
    sed -e 's|^linux-firmware-||' -e 's|\.tar\.xz$||' |
    sort | tail -1)" || latest=""
regdb_latest="$(curl -fsS --retry 3 "$regdb_archive/" |
    grep -oE 'wireless-regdb-[0-9]{4}\.[0-9]{2}\.[0-9]{2}\.tar\.xz' |
    sed -e 's|^wireless-regdb-||' -e 's|\.tar\.xz$||' |
    sort | tail -1)" || regdb_latest=""

printf '%s\t%s\t%s\t%s\n' linux-firmware "$pinned" "${latest:-?}" \
    "newest release tarball on kernel.org"
printf '%s\t%s\t%s\t%s\n' '  wireless-regdb' "$regdb_pinned" \
    "${regdb_latest:-?}" "regulatory.db, which cfg80211 declares"

[[ "${1:-}" == "--bump" ]] || exit 0

# The sums file is a clearsigned document, so the digest lines sit
# among the signature's own text. Matching the exact filename picks
# the one line that matters.
sums_digest() {
    curl -fsSL --retry 3 "$1/sha256sums.asc" |
        awk -v t="$2" '$2 == t { print $1 }'
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
    echo "kernel.org's sha256sums file stands behind these bytes"
    echo "licensing/sources.sh mirrors this tarball; run it with --repin"
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
echo "kernel.org's sha256sums file stands behind these bytes"
echo "the microcode domain builds AMD's blobs from this tarball; rebuild it too"
echo "next: make -C .. linux-firmware microcode"
