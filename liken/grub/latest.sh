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

pool="http://archive.ubuntu.com/ubuntu/pool/main/g/grub2"

pinned="$(cat "$here/VERSION")"

api="https://api.launchpad.net/devel/ubuntu"

# getPublishedSources lists one record for each series and pocket that
# publishes a version. A record's display_name reads like
# "grub2 2.14-2ubuntu3 in tenacious", and its pocket follows it in the
# same record. The Proposed pocket holds builds that wait for their
# tests, so they are not yet in the series.
launchpad() {
    curl -fsS --retry 3 --max-time 120 -G "$api/+archive/primary" \
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
newest_series="$(curl -fsS --retry 3 --max-time 60 "$api" |
    grep -oE '"current_series_link": "[^"]*"' |
    sed -e 's|.*/||' -e 's|"$||')" || newest_series=""

latest=""
[[ -z "$newest_series" ]] || latest="$(newest_in "$newest_series")" || latest=""
in_series=""
[[ -z "$pinned_series" ]] || in_series="$(newest_in "$pinned_series")" || in_series=""

versions="$(curl -fsS --retry 3 --max-time 60 "$pool/" |
    grep -oE 'grub-pc-bin_[^"_]+_amd64\.deb' |
    sed -e 's|^grub-pc-bin_||' -e 's|_amd64\.deb$||' |
    sort -uV)" || versions=""

note="${pinned_series:-its series} has ${in_series:-?}"
if [[ -n "$versions" ]] && ! grep -qxF "$pinned" <<<"$versions"; then
    note="the pin is gone from the pool"
fi

printf '%s\t%s\t%s\t%s\t%s\t%s\t%s\n' grub "$pinned" "${latest:-?}" \
    "${pinned_series:-?}" "${newest_series:-?}" - "$note"

[[ "${1:-}" == "--bump" ]] || exit 0

[[ -n "$versions" ]] || {
    echo "latest.sh: the pool listing did not answer" >&2
    exit 1
}
[[ -n "${2:-}" || -n "$latest" ]] || {
    echo "latest.sh: Launchpad named no version in the development series" >&2
    exit 1
}

target="${2:-$latest}"
grep -qxF "$target" <<<"$versions" || {
    echo "latest.sh: the pool does not carry $target" >&2
    exit 1
}
[[ "$target" != "$pinned" ]] || {
    echo "grub $pinned is current"
    exit 0
}

pc_bin="$(curl -fsSL --retry 3 --max-time 300 "$pool/grub-pc-bin_${target}_amd64.deb" |
    sha256sum | cut -d' ' -f1)"
common="$(curl -fsSL --retry 3 --max-time 300 "$pool/grub-common_${target}_amd64.deb" |
    sha256sum | cut -d' ' -f1)"

# The old entries stay. An older checkout still names its own version,
# and a digest table that forgets is a build that cannot be repeated.
echo "$target" >"$here/VERSION"
sed -i \
    -e "/^declare -A pc_bin_sha256=(/a\\    [\"$target\"]=\"$pc_bin\"" \
    -e "/^declare -A common_sha256=(/a\\    [\"$target\"]=\"$common\"" \
    "$here/fetch.sh"
echo "grub: $pinned -> $target"
echo "the digests came from these downloads; Ubuntu signs indexes, not files"
# A refused re-pin leaves the domain pin moved and the mirror
# behind it, which is a release that cannot serve its own
# sources. Say that plainly, because the fix is a hand edit.
"$here/../licensing/sources.sh" --repin || {
    echo "the pin moved; licensing/sources.sh still needs a hand edit" >&2
    exit 1
}
echo "next: make -C .. grub"
