#!/usr/bin/env bash
#
# Report the mpv release that the image builds, and the newest release
# upstream.
#
# The pin is an mpv release, and the bytes are the archive that GitHub
# serves for the release's tag, which the Dockerfile fetches by URL and
# checks against the sha256 in its MPV_SHA256 argument. `liken` builds
# mpv from source, so the pin has no channel, and both channel columns
# print `-`. Under the rule in root plan 83 it follows the newest mpv
# release. GitHub marks a pre-release, and its latest release is the
# newest one that is not. Every library mpv builds against comes from
# the Debian snapshot that vulkan/latest.sh reports.
#
# mpv publishes no checksum file. From 0.42.0 on, its release manager
# signs the tag's archive, and the release carries the signature as
# mpv-<version>.tar.gz.asc. The releases before 0.42.0 carry none, so
# the pinned 0.41.0 rests on the sha256 alone. The bump fetches the
# archive and the signature, and checks the signature against
# signing-keys.asc beside this script, which holds the key of Kacper
# Michajłow as his GitHub account and keys.openpgp.org publish it. Only
# then does it write the URL and the checksum into the Dockerfile, and
# the same facts with the purl and the CPE into pin.toml, which the
# image's SBOM record reads. A release with no signature stops the
# bump, and so does a signature by a key that the file does not hold,
# such as a new release manager's. A person then reads the new key and
# adds it.
#
# mpv links the snapshot's libplacebo, and a release states the oldest
# libplacebo that it builds with in its meson.build. A newer release
# whose libplacebo is newer than the snapshot's cannot build, so the
# row reads held with that reason, and the bump refuses. The
# libplacebo entry of package.toml states the snapshot's version, and
# vulkan/latest.sh --bump moves it with the snapshot.
#
# patches/ holds the upstream commits that the build applies to the
# release. A newer release can hold them, and a patch that its tree
# already holds does not apply, so the bump refuses while patches/
# holds a patch. The README beside this script gives the steps.
#
# Usage:
#   mpv/latest.sh          report the pin and the newest release
#   mpv/latest.sh --bump   write the newest release into the Dockerfile

set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source-path=SCRIPTDIR source=../toolchains/pin.sh
source "$here/../toolchains/pin.sh"
repository="mpv-player/mpv"

pinned="$(sed -n 's/^ARG MPV_VERSION=//p' "$here/Dockerfile")"

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

latest="$(github_api "repos/$repository/releases/latest" 2>/dev/null |
    jq -r '.tag_name // empty' | sed -n 's/^v\([0-9][0-9.]*\)$/\1/p')" || latest=""

# needs prints the oldest libplacebo that a release builds with, from
# the dependency line of its meson.build.
needs() {
    fetch_text "https://raw.githubusercontent.com/$repository/v$1/meson.build" 2>/dev/null |
        sed -n "s/^libplacebo = dependency('libplacebo', version: '>= *\([0-9.]*\)'.*/\1/p"
}

# The snapshot's libplacebo, without Debian's revision.
snapshot="$(sed -n 's/^libplacebo = "\([^"]*\)"$/\1/p' "$here/package.toml")"
snapshot="${snapshot%%-*}"

hold=-
needed=""
if [[ -n "$latest" && "$latest" != "$pinned" ]]; then
    needed="$(needs "$latest")" || needed=""
    if [[ -n "$needed" && "$(printf '%s\n' "$needed" "$snapshot" | sort -V | head -1)" != "$needed" ]]; then
        hold="$latest needs libplacebo $needed, and the snapshot has $snapshot"
    fi
fi

row mpv "$pinned" "$latest" - - "$hold" \
    "newest release on github.com/$repository; built in mpv/Dockerfile"

[[ "${1:-}" == "--bump" ]] || exit 0

bump_needed mpv "$pinned" "$latest" || exit 0
[[ -n "$needed" ]] || fail "the meson.build of mpv $latest did not answer, so the bump cannot tell which libplacebo it needs"
[[ "$hold" == - ]] || fail "mpv $hold. Move the snapshot to a date whose libplacebo is $needed or newer first."
patches=("$here"/patches/*.patch)
[[ ! -e "${patches[0]}" ]] ||
    fail "patches/ holds a patch. Delete each patch whose commit mpv $latest holds, with its URL in the backports of pin.toml, as mpv/README.md says, and run the bump again."

tag="v$latest"
url="https://github.com/$repository/archive/refs/tags/$tag.tar.gz"
tarball="mpv-$latest.tar.gz"
fetch_file "$url" "$work/$tarball"
fetch_file "https://github.com/$repository/releases/download/$tag/$tarball.asc" "$work/$tarball.asc"

verify_signature "$here/signing-keys.asc" "$work/$tarball.asc" "$work/$tarball"

sha256="$(sha256sum "$work/$tarball" | cut -d' ' -f1)"

set_line "$here/Dockerfile" '^ARG MPV_VERSION=' "ARG MPV_VERSION=$latest"
set_line "$here/Dockerfile" '^ARG MPV_URL=' "ARG MPV_URL=$url"
set_line "$here/Dockerfile" '^ARG MPV_SHA256=' "ARG MPV_SHA256=$sha256"
set_pin mpv "$latest" "$url" "$sha256" "$(github_purl "$repository" "$tag")" \
    "cpe:2.3:a:mpv:mpv:$latest:*:*:*:*:*:*:*"
set_toml "$here/package.toml" package.upstream mpv "\"$latest\""
bumped mpv "$pinned" "$latest"
echo "the signature of $tarball checks against signing-keys.asc"
next_step "raise mpv's revision, run make workflows, read the meson options in"
echo "mpv/Dockerfile against the release's meson.options, and run smoke/mpv.sh"
