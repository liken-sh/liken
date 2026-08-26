#!/usr/bin/env bash
#
# Report what this domain pins, and what IANA has released now.
#
# IANA serves the current release name, and nothing else, at one path.
# Comparing two short strings costs one request and needs no token,
# which is why the weekly workflow reads this script rather than
# keeping its own copy of the rule.
#
# The second pin is the image the zone files compile in. Two other
# domains pin the same image, and a bump here does not touch them.
#
# A bump writes the digests from bytes whose signature verified first.
# That ordering is the whole reason this domain pins a signing key: a
# digest that a robot computed from its own download proves nothing on
# its own, and IANA's signature is what stands behind these bytes.
# fetch.sh checks both again on every build.
#
# Usage:
#   tzdata/latest.sh                report both pins
#   tzdata/latest.sh --bump         move the database
#   tzdata/latest.sh --bump alpine  move the builder image

set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

releases="https://data.iana.org/time-zones/releases"

pinned="$(cat "$here/VERSION")"
# The builder pin is an image reference, and the digest is the part
# of it that moves. The tag beside it is a comment, because a fetch
# that named the tag would not be pinned at all.
pinned_builder="$(sed -n 's|^builder=".*@\(sha256:[^"]*\)".*|\1|p' "$here/fetch.sh")"
builder_tag="$(sed -n 's|^builder=.*# \(.*\)$|\1|p' "$here/fetch.sh")"

image_digest() {
    local token
    token="$(curl -fsS --retry 3 \
        "https://auth.docker.io/token?service=registry.docker.io&scope=repository:library/alpine:pull" |
        sed 's/.*"token":"//;s/".*//')"
    curl -fsSI --retry 3 -H "Authorization: Bearer $token" \
        -H "Accept: application/vnd.oci.image.index.v1+json,application/vnd.docker.distribution.manifest.list.v2+json" \
        "https://registry-1.docker.io/v2/library/alpine/manifests/$1" |
        grep -i '^docker-content-digest:' | tr -d '\r' | awk '{ print $2 }'
}

latest="$(curl -fsS --retry 3 https://data.iana.org/time-zones/tzdb/version |
    tr -d '[:space:]')" || latest=""
latest_builder="$(image_digest "$builder_tag")" || latest_builder=""

short() { echo "${1#sha256:}" | cut -c1-12; }

printf '%s\t%s\t%s\t%s\n' tzdata "$pinned" "${latest:-?}" \
    "the release IANA names as current"
printf '%s\t%s\t%s\t%s\n' '  alpine' \
    "$(short "$pinned_builder")" "$(short "${latest_builder:-?}")" \
    "the $builder_tag builder; open-iscsi, nfs-utils, and wpa-supplicant pin it too"

[[ "${1:-}" == "--bump" ]] || exit 0

if [[ "${2:-tzdata}" == "alpine" ]]; then
    [[ -n "$latest_builder" ]] || {
        echo "latest.sh: the registry did not answer" >&2
        exit 1
    }
    [[ "$latest_builder" != "$pinned_builder" ]] || {
        echo "the $builder_tag builder is current"
        exit 0
    }
    sed -i "s|^builder=\".*\"|builder=\"docker.io/library/alpine@$latest_builder\"|" \
        "$here/fetch.sh"
    echo "alpine: $(short "$pinned_builder") -> $(short "$latest_builder")"
    echo "open-iscsi, nfs-utils, and wpa-supplicant pin the same image; run their --bump alpine too"
    echo "next: make -C .. tzdata"
    exit 0
fi

[[ "${2:-tzdata}" == "tzdata" ]] || {
    echo "latest.sh: no pin named ${2}; try alpine" >&2
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
        curl -fsSL --retry 3 -o "$cache/$file" "$releases/$file"
    done
    gpgv --keyring "$cache/tz.gpg" "$cache/$name.asc" "$cache/$name"
done

echo "$latest" >"$here/VERSION"
(cd "$cache" && sha256sum "tzcode$latest.tar.gz" "tzdata$latest.tar.gz") >"$here/DIGESTS"

echo "tzdata: $pinned -> $latest"
echo "IANA's detached signatures stand behind these bytes"
echo "next: make -C .. tzdata"
