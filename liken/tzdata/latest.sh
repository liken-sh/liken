#!/usr/bin/env bash
#
# Report what this domain pins, and what IANA has released now.
#
# IANA serves the current release name, and nothing else, at one path.
# Comparing two short strings costs one request and needs no token.
#
# `liken` builds the zone files from IANA's source, so the pin has no
# channel, and both channel columns print `-`. Under the rule in root
# plan 83, the pin follows the release that IANA names as current.
# The image that the build runs in is the `alpine` entry in
# toolchains/images.toml, and toolchains/latest.sh reports it.
#
# A bump writes the digests from bytes whose signature verified first.
# That ordering is the whole reason this domain pins a signing key: a
# digest that a robot computed from its own download proves nothing on
# its own, and IANA's signature is what stands behind these bytes.
# fetch.sh checks both again on every build.
#
# Usage:
#   tzdata/latest.sh          report the pin
#   tzdata/latest.sh --bump   move the database

set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source-path=SCRIPTDIR source=../../toolchains/pin.sh
source "$here/../../toolchains/pin.sh"

releases="https://data.iana.org/time-zones/releases"

pinned="$(cat "$here/VERSION")"
latest="$(fetch_text https://data.iana.org/time-zones/tzdb/version |
    tr -d '[:space:]')" || latest=""
row tzdata "$pinned" "$latest" - - - "the release IANA names as current"

[[ "${1:-}" == "--bump" ]] || exit 0

[[ "${2:-tzdata}" == "tzdata" ]] || fail "this domain has one pin, tzdata"
bump_needed tzdata "$pinned" "$latest" || exit 0

require_tools sha256sum

# The tarballs land in the cache of the new version, where fetch.sh
# finds them, and each signature verifies against IANA's key in tz.asc
# before its tarball's digest is written.
cache="$here/cache/$latest"
for name in "tzcode$latest.tar.gz" "tzdata$latest.tar.gz"; do
    fetch_file "$releases/$name" "$cache/$name"
    fetch_file "$releases/$name.asc" "$cache/$name.asc"
    verify_signature "$here/tz.asc" "$cache/$name.asc" "$cache/$name"
done

echo "$latest" >"$here/VERSION"
(cd "$cache" && sha256sum "tzcode$latest.tar.gz" "tzdata$latest.tar.gz") >"$here/DIGESTS"

bumped tzdata "$pinned" "$latest"
echo "IANA's detached signatures stand behind these bytes"
next_step "make -C .. tzdata"
