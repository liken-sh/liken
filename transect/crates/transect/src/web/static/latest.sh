#!/usr/bin/env bash
#
# Report the htmx files that transect serves, and the newest release of
# the npm package htmx.org that they come from.
#
# htmx.min.js is dist/htmx.min.js of htmx.org, hx-sse.min.js is
# dist/ext/hx-sse.min.js of the same package, and htmx.LICENSE is the
# package's LICENSE. The binary embeds both scripts with include_bytes!,
# so the files are in the repository, and pin.toml beside this script
# names each one for the image's SBOM record.
#
# A channel is an npm dist-tag. htmx.org publishes its releases under
# more than one tag: `latest` and `next` have each named the newest
# major version at some time. The pin follows the tag whose release is
# the newest that is not a pre-release, so the report names that tag as
# both the channel and the newest channel.
#
# A new major version of htmx changes the attributes, the events, and
# the extensions that transect's templates and scripts use. So --bump
# stops when the newest release has another major version than the
# pin, and --bump --major writes it after the port. The bump-components
# skill's reference htmx.md gives the port and its proof.
#
# A bump downloads the package's tarball from the npm registry, checks
# it against the sha512 `integrity` that the registry gives for the
# release, and copies the files out of it. npm signs the registry's
# metadata, and this script does not check that signature, so each
# digest rests on the HTTPS connection to registry.npmjs.org.
#
# Usage:
#   latest.sh                  report the pin
#   latest.sh --bump           write the newest release of the same major version
#   latest.sh --bump --major   write the newest release of any major version

set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source-path=SCRIPTDIR source=../../../../../../toolchains/pin.sh
source "$here/../../../../../../toolchains/pin.sh"

require_tools jq tar sha256sum base64 xxd

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

package=htmx.org
# Each entry of pin.toml and the file of the package that it names. The
# file in this directory has the same name as the last part of the path.
entries=(htmx hx-sse)
declare -A file=([htmx]=dist/htmx.min.js [hx-sse]=dist/ext/hx-sse.min.js)

# newest_channel <tags file>: print the tag whose release is the
# newest, of the tags whose release is not a pre-release.
newest_channel() {
    awk '$2 !~ /-/ { print $2, $1 }' "$1" | sort -V | tail -1 | cut -d' ' -f2
}

fetch_text "https://registry.npmjs.org/$package" --max-time 30 |
    jq -r '."dist-tags" | to_entries[] | "\(.key) \(.value)"' >"$work/tags" || : >"$work/tags"
channel="$(newest_channel "$work/tags")"
newest="$(awk -v tag="$channel" '$1 == tag { print $2 }' "$work/tags")"
pinned="$(pin_value htmx version)"
row htmx "$pinned" "$newest" "${channel:-?}" "${channel:-?}" - \
    "$package: htmx.min.js and hx-sse.min.js, the release of the newest npm dist-tag"

[[ "${1:-}" == "--bump" ]] || exit 0
bump_needed htmx "$pinned" "$newest" || exit 0
if [[ "${pinned%%.*}" != "${newest%%.*}" && "${2:-}" != "--major" ]]; then
    fail "htmx $newest is a new major version, which needs a port of the templates first;" \
        "see .agents/skills/bump-components/references/htmx.md, then run latest.sh --bump --major"
fi

meta="$(fetch_text "https://registry.npmjs.org/$package/$newest" --max-time 30)"
tarball="$(jq -r .dist.tarball <<<"$meta")"
integrity="$(jq -r .dist.integrity <<<"$meta")"
[[ "$integrity" == sha512-* ]] || fail "$package $newest has no sha512 integrity"
sha512="$(base64 -d <<<"${integrity#sha512-}" | xxd -p -c 64)"

fetch_file "$tarball" "$work/package.tgz"
digest_matches "$sha512" "$work/package.tgz" ||
    fail "$tarball does not match the integrity that the registry gives"
tar -xzf "$work/package.tgz" -C "$work" "${file[@]/#/package/}" package/LICENSE
cp "$work/package/LICENSE" "$here/htmx.LICENSE"
for entry in "${entries[@]}"; do
    path="${file[$entry]}"
    name="$(basename "$path")"
    cp "$work/package/$path" "$here/$name"
    sha256="$(sha256sum "$here/$name" | cut -d' ' -f1)"
    purl="pkg:npm/$package@$newest"
    [[ "$entry" == htmx ]] || purl="$purl#$path"
    set_pin "$entry" "$newest" "https://cdn.jsdelivr.net/npm/$package@$newest/$path" "$sha256" "$purl"
done
bumped htmx "$pinned" "$newest"

(cd "$here" && sha256sum htmx.min.js hx-sse.min.js htmx.LICENSE >htmx.sha256)
next_step "update the versions in the comments of ../assets.rs, then make -C ../../../../.. test"
