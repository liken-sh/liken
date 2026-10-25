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

releases="https://data.iana.org/time-zones/releases"

pinned="$(cat "$here/VERSION")"
latest="$(curl -fsS --retry 3 --max-time 60 https://data.iana.org/time-zones/tzdb/version |
    tr -d '[:space:]')" || latest=""
printf '%s\t%s\t%s\t%s\t%s\t%s\t%s\n' tzdata "$pinned" "${latest:-?}" - - - \
    "the release IANA names as current"

[[ "${1:-}" == "--bump" ]] || exit 0

[[ "${2:-tzdata}" == "tzdata" ]] || {
    echo "latest.sh: this domain has one pin, tzdata" >&2
    exit 1
}
[[ -n "$latest" ]] || {
    echo "latest.sh: IANA did not answer" >&2
    exit 1
}
[[ "$latest" != "$pinned" ]] || {
    echo "tzdata $pinned is current"
    exit 0
}

for tool in curl gpg gpgv sha256sum; do
    command -v "$tool" >/dev/null || {
        echo "latest.sh: missing required tool: $tool" >&2
        exit 1
    }
done

# gpgv reads a keyring, not an armored key, and the repository keeps
# the key in its armored form because that is the form a person can
# read and compare against IANA's published fingerprint.
cache="$here/cache/$latest"
mkdir -p "$cache"
gpg --dearmor <"$here/tz.asc" >"$cache/tz.gpg"
for name in "tzcode$latest.tar.gz" "tzdata$latest.tar.gz"; do
    for file in "$name" "$name.asc"; do
        curl -fsSL --retry 3 --max-time 300 -o "$cache/$file" "$releases/$file"
    done
    gpgv --keyring "$cache/tz.gpg" "$cache/$name.asc" "$cache/$name"
done

echo "$latest" >"$here/VERSION"
(cd "$cache" && sha256sum "tzcode$latest.tar.gz" "tzdata$latest.tar.gz") >"$here/DIGESTS"

echo "tzdata: $pinned -> $latest"
echo "IANA's detached signatures stand behind these bytes"
echo "next: make -C .. tzdata"
