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
api="https://gitlab.freedesktop.org/api/v4/projects/wayland%2Fweston/releases"

pinned="$(sed -n 's/^ARG WESTON_VERSION=//p' "$here/Dockerfile")"

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

curl -fsS --retry 3 --max-time 60 "$api?per_page=50" >"$work/releases.json" 2>/dev/null ||
    echo '[]' >"$work/releases.json"
latest="$(jq -r '.[] | select(.upcoming_release | not) | .tag_name' "$work/releases.json" |
    grep -E '^[0-9]+\.[0-9]+\.[0-9]+$' | awk -F. '$3 < 90' | sort -V | tail -1)" || latest=""

printf '%s\t%s\t%s\t%s\t%s\t%s\t%s\n' weston "$pinned" "${latest:-?}" - - - \
    "newest release on gitlab.freedesktop.org; built in weston/Dockerfile"

[[ "${1:-}" == "--bump" ]] || exit 0

[[ -n "$latest" ]] || {
    echo "latest.sh: gitlab.freedesktop.org did not answer" >&2
    exit 1
}
[[ "$latest" != "$pinned" ]] || {
    echo "weston $pinned is current"
    exit 0
}
[[ "${latest%%.*}" == "${pinned%%.*}" ]] || {
    echo "latest.sh: weston $latest is a new major release. Move libweston-${pinned%%.*}," >&2
    echo "weston-closure.sh and layout/ivi-layout-export.h by hand." >&2
    exit 1
}

asset() {
    jq -r --arg t "$latest" --arg n "$1" \
        '.[] | select(.tag_name == $t) | .assets.links[] | select(.name == $n) | .url' \
        "$work/releases.json"
}
tarball="weston-$latest.tar.xz"
url="$(asset "$tarball")"
[[ "$url" == https://* ]] || {
    echo "latest.sh: the release $latest has no $tarball" >&2
    exit 1
}
curl -fsSL --retry 3 --max-time 300 -o "$work/$tarball" "$url"
curl -fsSL --retry 3 --max-time 60 -o "$work/$tarball.sig" "$(asset "$tarball.sig")"
curl -fsSL --retry 3 --max-time 60 -o "$work/$tarball.sha256sum" "$(asset "$tarball.sha256sum")"

export GNUPGHOME="$work/gnupg"
mkdir -m 700 "$GNUPGHOME"
gpg --quiet --import "$here/signing-keys.asc" 2>/dev/null
gpg --quiet --verify "$work/$tarball.sig" "$work/$tarball"

sha256="$(sha256sum "$work/$tarball" | cut -d' ' -f1)"
[[ "$(cut -d' ' -f1 "$work/$tarball.sha256sum")" == "$sha256" ]] || {
    echo "latest.sh: $tarball does not match the release's sha256sum file" >&2
    exit 1
}

sed -i -e "s|^ARG WESTON_VERSION=.*|ARG WESTON_VERSION=$latest|" \
    -e "s|^ARG WESTON_URL=.*|ARG WESTON_URL=$url|" \
    -e "s|^ARG WESTON_SHA256=.*|ARG WESTON_SHA256=$sha256|" "$here/Dockerfile"
sed -i "/^\[package\.upstream\]$/,/^\[/ s/^weston = \".*\"$/weston = \"$latest\"/" "$here/package.toml"
echo "weston: $pinned -> $latest"
echo "the signature of $tarball checks against signing-keys.asc"
echo "next: raise weston's revision and display-operator's, run make workflows, and run layout/smoke.sh"
