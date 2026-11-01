#!/usr/bin/env bash
#
# Report the weston release that the image builds, and the newest
# release upstream.
#
# The pin is a weston release, and the bytes are its tarball on
# gitlab.freedesktop.org, which the Dockerfile fetches by URL and
# checks against the sha256 in its WESTON_SHA256 argument. `liken`
# builds weston from source, so the pin has no channel, and both
# channel columns print `-`. Under the rule in root plan 83 it follows
# the newest weston release. Every library weston builds against comes
# from the Debian snapshot that vulkan/latest.sh reports.
#
# weston numbers a pre-release of X.0.0 as X-1.0.9N, such as 15.0.93
# for the third release candidate of 16.0.0, so a release whose third
# number is 90 or more is not a release.
#
# The bump fetches the tarball and its signature from the release's
# assets and checks the signature against signing-keys.asc beside this
# script, which holds the keys of weston's release managers as
# Debian's package of weston lists them in debian/upstream. It checks
# the tarball against the release's sha256sum file too, and only then
# writes the URL and the checksum into the Dockerfile. A new major
# release changes libweston's soname and the copy of
# ivi-layout-export.h, so the bump stops there and a person moves it.
#
# Usage:
#   weston/latest.sh          report the pin and the newest release
#   weston/latest.sh --bump   write the newest release into the Dockerfile

set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source-path=SCRIPTDIR source=../toolchains/pin.sh
source "$here/../toolchains/pin.sh"
api="https://gitlab.freedesktop.org/api/v4/projects/wayland%2Fweston/releases"

pinned="$(sed -n 's/^ARG WESTON_VERSION=//p' "$here/Dockerfile")"

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

fetch_text "$api?per_page=50" >"$work/releases.json" 2>/dev/null ||
    echo '[]' >"$work/releases.json"
latest="$(jq -r '.[] | select(.upcoming_release | not) | .tag_name' "$work/releases.json" |
    grep -E '^[0-9]+\.[0-9]+\.[0-9]+$' | awk -F. '$3 < 90' | sort -V | tail -1)" || latest=""

row weston "$pinned" "$latest" - - - \
    "newest release on gitlab.freedesktop.org; built in weston/Dockerfile"

[[ "${1:-}" == "--bump" ]] || exit 0

bump_needed weston "$pinned" "$latest" || exit 0
[[ "${latest%%.*}" == "${pinned%%.*}" ]] ||
    fail "weston $latest is a new major release. Move libweston-${pinned%%.*}, weston-closure.sh and layout/ivi-layout-export.h by hand."

asset() {
    jq -r --arg t "$latest" --arg n "$1" \
        '.[] | select(.tag_name == $t) | .assets.links[] | select(.name == $n) | .url' \
        "$work/releases.json"
}
tarball="weston-$latest.tar.xz"
url="$(asset "$tarball")"
[[ "$url" == https://* ]] || fail "the release $latest has no $tarball"
fetch_file "$url" "$work/$tarball"
fetch_file "$(asset "$tarball.sig")" "$work/$tarball.sig"
fetch_file "$(asset "$tarball.sha256sum")" "$work/$tarball.sha256sum"

verify_signature "$here/signing-keys.asc" "$work/$tarball.sig" "$work/$tarball"

sha256="$(sha256sum "$work/$tarball" | cut -d' ' -f1)"
[[ "$(cut -d' ' -f1 "$work/$tarball.sha256sum")" == "$sha256" ]] ||
    fail "$tarball does not match the release's sha256sum file"

set_line "$here/Dockerfile" '^ARG WESTON_VERSION=' "ARG WESTON_VERSION=$latest"
set_line "$here/Dockerfile" '^ARG WESTON_URL=' "ARG WESTON_URL=$url"
set_line "$here/Dockerfile" '^ARG WESTON_SHA256=' "ARG WESTON_SHA256=$sha256"
set_toml "$here/package.toml" package.upstream weston "\"$latest\""
bumped weston "$pinned" "$latest"
echo "the signature of $tarball checks against signing-keys.asc"
next_step "raise weston's revision and display-operator's, run make workflows, and run layout/smoke.sh"
