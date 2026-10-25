#!/usr/bin/env bash
#
# Report the snapshot date that the INDI images install from, the
# Ubuntu series they install, and what the newest snapshot holds.
#
# The indi images install from three archives on one date: Ubuntu at
# snapshot.ubuntu.com, and the INDI PPA, mutlaqja/ppa, and the PHD2
# PPA, pch/phd2, at snapshot.ppa.launchpadcontent.net. package.toml
# states the date as its version, and lists under [package.upstream]
# the INDI, INDI third-party, GSC, and PHD2 releases that the date
# installs. snapshot.sh names the Ubuntu series.
#
# Each PPA is its own repository, and its newest channel is the newest
# Ubuntu series it publishes. The images install from both PPAs, so
# the newest channel here is the newest series that both publish.
# Ubuntu's own development series is often ahead of them, and the
# images cannot move to a series that the PPAs do not build for.
#
# A snapshot date names the archive as it stood at midnight UTC of
# that day, so the files of a date stop changing at that midnight.
# Today's date in UTC is the newest date whose files are fixed. The
# upstream rows read the PPAs' Packages indexes at that date, in the
# newest channel, so each row shows what a bump installs.
#
# Usage:
#   indi/latest.sh          report the date, the series, and the
#                           upstream releases
#   indi/latest.sh --bump   move the date, set the revision to 1, and
#                           write the upstream releases; the other
#                           steps are in the bump-components skill

set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

row() { printf '%s\t%s\t%s\t%s\t%s\t%s\t%s\n' "$@"; }

ppas=(mutlaqja/ppa pch/phd2)

pinned="$(sed -n 's/^version = "\([0-9]*\)"$/\1/p' "$here/package.toml")"
series="$(sed -n 's/^Suites: \([a-z]*\)$/\1/p' "$here/snapshot.sh" | head -1)"
latest="$(date -u +%Y%m%d)"

# Each [package.upstream] entry names a release, and the smoke checks
# compare it with one binary package of one PPA. smoke/lib.sh pairs
# the same packages with the entries.
package_of() {
    case "$1" in
    indi) echo "mutlaqja/ppa indi-bin" ;;
    indi-3rdparty) echo "mutlaqja/ppa indi-3rdparty-drivers" ;;
    gsc) echo "mutlaqja/ppa gsc" ;;
    phd2) echo "pch/phd2 phd2" ;;
    esac
}

upstreams() {
    awk '/^\[/ { s = $0; next } s == "[package.upstream]" && / = / { gsub(/"/, "", $3); print $1, $3 }' \
        "$here/package.toml"
}

# The newest Ubuntu series that every PPA publishes. Launchpad lists
# each series with its version, and a PPA that builds for a series
# serves a Release file under its name.
newest_series() {
    local version name ppa
    curl -fsS --retry 3 --max-time 20 https://api.launchpad.net/devel/ubuntu/series |
        jq -r '.entries[] | "\(.version) \(.name)"' | sort -rV >"$work/series"
    while read -r version name; do
        for ppa in "${ppas[@]}"; do
            curl -fs -o /dev/null --max-time 20 \
                "https://ppa.launchpadcontent.net/$ppa/ubuntu/dists/$name/Release" || continue 2
        done
        echo "$name"
        return
    done <"$work/series"
}

newest="$(newest_series)" || newest=""

# The Packages index of one PPA at the newest date, in one series, as
# "package version".
packages() {
    curl -fsSL --retry 3 --max-time 60 \
        "https://snapshot.ppa.launchpadcontent.net/$1/ubuntu/${latest}T000000Z/dists/$2/main/binary-amd64/Packages.gz" |
        gunzip | awk '/^Package:/ { p = $2 } /^Version:/ { print p, $2 }'
}

if [[ -n "$newest" ]]; then
    for ppa in "${ppas[@]}"; do
        packages "$ppa" "$newest" >"$work/${ppa//\//_}" || : >"$work/${ppa//\//_}"
    done
    [[ -s "$work/mutlaqja_ppa" ]] || latest=""
else
    latest=""
fi

newest_version() {
    local ppa package
    read -r ppa package < <(package_of "$1")
    [[ -n "${ppa:-}" && -s "$work/${ppa//\//_}" ]] || return 0
    awk -v p="$package" '$1 == p { print $2 }' "$work/${ppa//\//_}" | sort -V | tail -1
}

row snapshot "$pinned" "${latest:-?}" "$series" "${newest:-?}" - \
    "the date of the Ubuntu, mutlaqja/ppa, and pch/phd2 snapshots"
upstreams | while read -r name version; do
    now="$(newest_version "$name")"
    row "  $name" "$version" "${now:-?}" - - - "$(package_of "$name" | awk '{ print $2 " from " $1 }')"
done

[[ "${1:-}" == "--bump" ]] || exit 0

[[ -n "$latest" && -n "$newest" ]] || {
    echo "latest.sh: Launchpad or the PPA snapshots did not answer" >&2
    exit 1
}
# A new series changes snapshot.sh, the ubuntu entry in
# toolchains/images.toml, and every version suffix, so it is a hand
# edit and a separate step.
[[ "$newest" == "$series" ]] || {
    echo "latest.sh: the PPAs publish $newest; move snapshot.sh and the ubuntu image from $series by hand first" >&2
    exit 1
}
[[ "$latest" != "$pinned" ]] || {
    echo "indi $pinned is current"
    exit 0
}

sed -i -e "s/^version = \"$pinned\"$/version = \"$latest\"/" \
    -e 's/^revision = [0-9]*$/revision = 1/' "$here/package.toml"
upstreams | while read -r name old; do
    new="$(newest_version "$name")"
    [[ -n "$new" ]] || {
        echo "latest.sh: no version of $name in the snapshot" >&2
        exit 1
    }
    sed -i "/^\[package\.upstream\]$/,/^\[/ s/^$name = \".*\"$/$name = \"$new\"/" "$here/package.toml"
    [[ "$old" == "$new" ]] || echo "$name: $old -> $new"
done
echo "indi: $pinned -> $latest"
echo "next: make workflows at the top of the repository, then the steps of section 5a"
echo "of the bump-components skill: the SDK licenses, the builds and smoke checks, and"
echo "make -C observatory-operator drivers firmware-rules captures schema crosscheck"
