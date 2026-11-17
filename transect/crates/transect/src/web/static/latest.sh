#!/usr/bin/env bash
#
# Report the two htmx files that transect serves, and the newest
# release of each npm package that they come from.
#
# htmx.min.js is dist/htmx.min.js of the package htmx.org, and
# sse.min.js is dist/sse.min.js of the package htmx-ext-sse. The binary
# embeds both with include_bytes!, so the files are in the repository,
# and pin.toml beside this script names each one for the image's SBOM
# record.
#
# A channel is an npm dist-tag. npm installs the release that the tag
# `latest` names, and htmx.org also publishes a newer major version
# under the tag `next`, so the report names `next` as the newest
# channel when its release is newer and not a pre-release. The htmx pin
# is a hold on `latest`: htmx 4 is a new major version, and transect's
# templates, its stream.js, and the htmx-ext-sse extension are written
# for htmx 2. Moving to htmx 4 is a port, which no bump does.
#
# A bump downloads the package's tarball from the npm registry, checks
# it against the sha512 `integrity` that the registry gives for the
# release, and copies the file and the package's LICENSE out of it.
# npm signs the registry's metadata, and this script does not check
# that signature, so each digest rests on the HTTPS connection to
# registry.npmjs.org.
#
# Usage:
#   latest.sh          report both pins
#   latest.sh --bump   write the newest release of `latest` of each

set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source-path=SCRIPTDIR source=../../../../../../toolchains/pin.sh
source "$here/../../../../../../toolchains/pin.sh"

require_tools jq tar sha256sum base64 xxd

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

# Each pin: the manifest entry, the npm package, the file of the
# package, and the file in this directory.
entries=(htmx htmx-ext-sse)
declare -A package=([htmx]=htmx.org [htmx-ext-sse]=htmx-ext-sse)
declare -A file=([htmx]=htmx.min.js [htmx-ext-sse]=sse.min.js)
declare -A license=([htmx]=htmx.LICENSE [htmx-ext-sse]=sse.LICENSE)

# tags <package>: print the package's dist-tags, one "tag version" on
# each line.
tags() {
    fetch_text "https://registry.npmjs.org/$1" --max-time 30 |
        jq -r '."dist-tags" | to_entries[] | "\(.key) \(.value)"'
}

# newest_channel <tags file>: print the tag whose release is the
# newest, of the tags whose release is not a pre-release.
newest_channel() {
    awk '$2 !~ /-/ { print $2, $1 }' "$1" | sort -V | tail -1 | cut -d' ' -f2
}

hold() {
    case "$1" in
    htmx) echo "htmx 4 needs a port of the templates, stream.js, and the SSE extension" ;;
    *) echo - ;;
    esac
}

for entry in "${entries[@]}"; do
    pinned="$(pin_value "$entry" version)"
    tags "${package[$entry]}" >"$work/$entry.tags" || : >"$work/$entry.tags"
    latest="$(awk '$1 == "latest" { print $2 }' "$work/$entry.tags")"
    newest="$(newest_channel "$work/$entry.tags")"
    row "$entry" "$pinned" "$latest" latest "${newest:-?}" "$(hold "$entry")" \
        "the release of the npm dist-tag latest of ${package[$entry]}"
done

[[ "${1:-}" == "--bump" ]] || exit 0

for entry in "${entries[@]}"; do
    pinned="$(pin_value "$entry" version)"
    latest="$(awk '$1 == "latest" { print $2 }' "$work/$entry.tags")"
    bump_needed "$entry" "$pinned" "$latest" || continue

    name="${package[$entry]}"
    meta="$(fetch_text "https://registry.npmjs.org/$name/$latest" --max-time 30)"
    tarball="$(jq -r .dist.tarball <<<"$meta")"
    integrity="$(jq -r .dist.integrity <<<"$meta")"
    [[ "$integrity" == sha512-* ]] || fail "$name $latest has no sha512 integrity"
    sha512="$(base64 -d <<<"${integrity#sha512-}" | xxd -p -c 64)"

    fetch_file "$tarball" "$work/$entry.tgz"
    digest_matches "$sha512" "$work/$entry.tgz" ||
        fail "$tarball does not match the integrity that the registry gives"
    mkdir -p "$work/$entry"
    tar -xzf "$work/$entry.tgz" -C "$work/$entry" "package/dist/${file[$entry]}" package/LICENSE
    cp "$work/$entry/package/dist/${file[$entry]}" "$here/${file[$entry]}"
    cp "$work/$entry/package/LICENSE" "$here/${license[$entry]}"

    sha256="$(sha256sum "$here/${file[$entry]}" | cut -d' ' -f1)"
    set_pin "$entry" "$latest" "https://cdn.jsdelivr.net/npm/$name@$latest/dist/${file[$entry]}" \
        "$sha256" "pkg:npm/$name@$latest"
    bumped "$entry" "$pinned" "$latest"
done

(cd "$here" && sha256sum htmx.min.js htmx.LICENSE sse.min.js sse.LICENSE >htmx.sha256)
next_step "update the versions in the comments of crates/transect/src/web/assets.rs, then make -C transect test"
