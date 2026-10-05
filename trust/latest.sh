#!/usr/bin/env bash
#
# Report what this domain pins, and what snapshot curl has published
# now.
#
# The CA bundle has no version number. Each snapshot is named by the
# date Mozilla's store was extracted, and curl keeps every one of
# them. The extract page is the index of those dates, so the newest
# name on that page is the newest snapshot.
#
# Read what changed before you take a bump. A snapshot removes trust
# as well as adding it, and a certificate authority that leaves this
# file is one that every machine stops trusting on its next boot.
#
# The OS build's fetch reads curl's .sha256 file beside each snapshot,
# and the trust image fetches by the same digest, written into its
# Dockerfile by --bump.
#
# Usage:
#   trust/latest.sh          report the pin and the newest snapshot
#   trust/latest.sh --bump   write the newest snapshot into VERSION,
#                            package.toml, and the Dockerfile

set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

extract="https://curl.se/docs/caextract.html"

pinned="$(cat "$here/VERSION")"

# The names are dates in ISO order, so a plain sort finds the newest.
latest="$(curl -fsS --retry 3 "$extract" |
    grep -oE 'cacert-[0-9]{4}-[0-9]{2}-[0-9]{2}\.pem' |
    sed -e 's|^cacert-||' -e 's|\.pem$||' |
    sort | tail -1)" || latest=""

printf '%s\t%s\t%s\t%s\n' trust "$pinned" "${latest:-?}" \
    "newest Mozilla extract that curl publishes"

[[ "${1:-}" == "--bump" ]] || exit 0

[[ -n "$latest" ]] || {
    echo "latest.sh: the extract page did not answer" >&2
    exit 1
}
[[ "$latest" != "$pinned" ]] || {
    echo "the CA bundle $pinned is current"
    exit 0
}

# The pin is the date, in the three places that state it: VERSION, which
# the OS build reads; the version of the trust image in package.toml; and
# the snapshot that the Dockerfile fetches by its checksum. A new date
# starts the image's revisions at 1 again.
digest="$(curl -fsS --retry 3 "https://curl.se/ca/cacert-$latest.pem.sha256" | cut -d' ' -f1)"
[[ "$digest" =~ ^[0-9a-f]{64}$ ]] || {
    echo "latest.sh: curl.se published no sha256 for $latest" >&2
    exit 1
}
echo "$latest" >"$here/VERSION"
sed -i -e "s/^version = \"[0-9]*\"$/version = \"${latest//-/}\"/" \
    -e "s/^revision = [0-9]*$/revision = 1/" \
    -e "s/^mozilla-ca = \".*\"$/mozilla-ca = \"$latest\"/" "$here/package.toml"
sed -i -e "s/--checksum=sha256:[0-9a-f]*/--checksum=sha256:$digest/" \
    -e "s|cacert-[0-9-]*\.pem|cacert-$latest.pem|" "$here/Dockerfile"
echo "trust: $pinned -> $latest"
echo "curl's .sha256 file stands behind these bytes"
echo "next: make workflows at the top of the repository, raise the revision"
echo "of each pinned component that copies the bundle, and make -C ../liken trust"
