#!/usr/bin/env bash
#
# Report what this domain pins, and what Ubuntu's development
# series carries now.
#
# The pin is a version of Ubuntu's `grub2` source package, and the bytes
# are Ubuntu's prebuilt `grub-pc-bin` deb from the archive's pool.
# Ubuntu publishes each package in several series at once, and each
# series carries its own upstream GRUB. Under the rule in root plan 83,
# a pin from a package repository follows the newest series that the
# repository offers to install whole, and for Ubuntu that is the
# development series. So the channel column is the series that publishes
# the pinned version, and the newest-channel column is the development
# series that Launchpad names as current. The latest column is the
# version in the development series, and a bare `--bump` moves the pin
# to it, across series when it must. The note names the newest build in
# the pin's own series, which is the security update to take while the
# pin stays on that series.
#
# Launchpad's API answers both questions, as JSON. The script reads
# the two fields it needs with grep, so its tool list stays at curl.
#
# The pool is the one upstream here that forgets. It keeps only the
# current build of each series, so a superseded pin returns a 404
# error and the domain stops building. A pin that is no longer in the
# pool's listing is reported as gone.
#
# This domain fetches two debs and pins both. They always share a
# version, because they come from one source package, so one bump
# writes both digests.
#
# Usage:
#   grub/latest.sh                       report the pin
#   grub/latest.sh --bump                take the version in the
#                                        development series
#   grub/latest.sh --bump 2.12-1ubuntu7.3
#                                        take another version that the
#                                        pool carries

set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source-path=SCRIPTDIR source=../../toolchains/pin.sh
source "$here/../../toolchains/pin.sh"

pool="http://archive.ubuntu.com/ubuntu/pool/main/g/grub2"

pinned="$(cat "$here/VERSION")"

api="https://api.launchpad.net/devel/ubuntu"

# getPublishedSources lists one record for each series and pocket that
# publishes a version. A record's display_name reads like
# "grub2 2.14-2ubuntu3 in tenacious", and its pocket follows it in the
# same record. The Proposed pocket holds builds that wait for their
# tests, so they are not yet in the series.
launchpad() {
    fetch_text "$api/+archive/primary" --max-time 120 -G \
        --data ws.op=getPublishedSources --data source_name=grub2 \
        --data exact_match=true "$@" |
        grep -oE '"display_name": "[^"]*"|"pocket": "[^"]*"' |
        paste - - |
        grep -v '"pocket": "Proposed"' |
        sed -E 's|^"display_name": "[^ ]+ ([^ ]+) in ([^"]+)".*|\1 \2|'
}

# The newest version that a series publishes now.
newest_in() {
    launchpad --data status=Published --data "distro_series=$api/$1" |
        cut -d' ' -f1 | sort -V | tail -1
}

pinned_series="$(launchpad --data "version=$pinned" | head -1 | cut -d' ' -f2)" ||
    pinned_series=""
newest_series="$(fetch_text "$api" |
    grep -oE '"current_series_link": "[^"]*"' |
    sed -e 's|.*/||' -e 's|"$||')" || newest_series=""

latest=""
[[ -z "$newest_series" ]] || latest="$(newest_in "$newest_series")" || latest=""
in_series=""
[[ -z "$pinned_series" ]] || in_series="$(newest_in "$pinned_series")" || in_series=""

versions="$(fetch_text "$pool/" |
    grep -oE 'grub-pc-bin_[^"_]+_amd64\.deb' |
    sed -e 's|^grub-pc-bin_||' -e 's|_amd64\.deb$||' |
    sort -uV)" || versions=""

note="${pinned_series:-its series} has ${in_series:-?}"
if [[ -n "$versions" ]] && ! grep -qxF "$pinned" <<<"$versions"; then
    note="the pin is gone from the pool"
fi

row grub "$pinned" "$latest" \
    "${pinned_series:-?}" "${newest_series:-?}" - "$note"

[[ "${1:-}" == "--bump" ]] || exit 0

[[ -n "$versions" ]] || fail "the pool listing did not answer"
[[ -n "${2:-}" || -n "$latest" ]] || fail "Launchpad named no version in the development series"

target="${2:-$latest}"
grep -qxF "$target" <<<"$versions" || fail "the pool does not carry $target"
bump_needed grub "$pinned" "$target" || exit 0

pc_bin="$(digest_of_url "$pool/grub-pc-bin_${target}_amd64.deb")"
# grub-mkimage is in grub2-common from the 2.14 packaging on, and
# fetch.sh takes that package for every version it does not name.
common="$(digest_of_url "$pool/grub2-common_${target}_amd64.deb")"

echo "$target" >"$here/VERSION"
add_entry "$here/fetch.sh" pc_bin_sha256 "$target" "$pc_bin"
add_entry "$here/fetch.sh" common_sha256 "$target" "$common"
bumped grub "$pinned" "$target"
echo "the digests came from these downloads; Ubuntu signs indexes, not files"
repin_sources
next_step "make -C .. grub"
