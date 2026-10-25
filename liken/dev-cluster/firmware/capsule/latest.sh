#!/usr/bin/env bash
#
# Report what the capsule builder pins: the edk2 release that build.sh
# checks out, the Ubuntu image that the Dockerfile builds on, and the
# date of the Ubuntu snapshot that it installs the compiler, nasm, and
# iasl from.
#
# edk2 builds from source, so its newest channel is the newest
# edk2-stable release. build.sh keeps the release that Debian's
# ovmf-generic packages, because the other FIRMWARE modes boot that
# package and a guest keeps its variable store only when both builds
# use one layout. So a newer edk2 release than ovmf-generic packages
# reads as a hold until ovmf-generic moves.
#
# The Ubuntu image and the snapshot take the newest channel of the
# Ubuntu archive, which is its development series. A snapshot date
# names the archive as it stood at midnight UTC of that day, so
# today's date in UTC is the newest date whose files are fixed, and
# the snapshot row reads behind each day.
#
# A move of any of the three changes the toolchain that builds the
# firmware, so a bump is a hand edit of build.sh or the Dockerfile,
# followed by a boot of the lab with FIRMWARE=capsule, which builds the
# firmware again because the Makefile lists both files as its inputs.
#
# Usage:
#   dev-cluster/firmware/capsule/latest.sh   report the pins

set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

row() { printf '%s\t%s\t%s\t%s\t%s\t%s\t%s\n' "$@"; }

# The GitHub API allows 60 anonymous requests an hour, so the report
# sends a token when one is at hand.
github_auth=()
token="${GITHUB_TOKEN:-$(gh auth token 2>/dev/null || true)}"
[[ -n "$token" ]] && github_auth=(-H "Authorization: Bearer $token")

edk2="$(sed -n 's/^edk2_tag="\(.*\)"$/\1/p' "$here/build.sh")"
edk2_latest="$(curl -fsS --retry 3 --max-time 20 "${github_auth[@]}" \
    https://api.github.com/repos/tianocore/edk2/releases/latest | jq -r .tag_name)" || edk2_latest=""

# ovmf-generic 2026.08+ds-2 packages edk2-stable202608.
ovmf="$(cat "$here/../VERSION")"
year="${ovmf%%.*}"
month="${ovmf#*.}"
ovmf_release="edk2-stable$year${month%%+*}"
hold=-
if [[ -n "$edk2_latest" && "$edk2_latest" > "$ovmf_release" ]]; then
    hold="ovmf-generic $ovmf packages $ovmf_release, and the capsule firmware keeps its variable store layout"
fi
row edk2 "$edk2" "${edk2_latest:-?}" - - "$hold" "the edk2 release that the capsule firmware builds from"

# Launchpad lists each series with its version. The newest one is the
# development series.
series="$(curl -fsS --retry 3 --max-time 20 https://api.launchpad.net/devel/ubuntu/series |
    jq -r '.entries[] | "\(.version) \(.name)"' | sort -V)" || series=""
read -r newest_version newest_name < <(tail -1 <<<"$series") || true

from="$(sed -n 's/^FROM ubuntu:\([^@]*\)@.*$/\1/p' "$here/Dockerfile")"
suite="$(sed -n "s/^ *'Suites: \([a-z]*\) .*$/\1/p" "$here/Dockerfile")"
row ubuntu "$from" "${newest_version:-?}" "$suite" "${newest_name:-?}" - \
    "the base of the capsule builder"

snapshot="$(sed -n 's/^ENV SNAPSHOT=\([0-9]*\)$/\1/p' "$here/Dockerfile")"
row snapshot "$snapshot" "$(date -u +%Y%m%d)" "$suite" "${newest_name:-?}" - \
    "the Ubuntu snapshot that the builder installs gcc, nasm, and iasl from"
