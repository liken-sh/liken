#!/usr/bin/env bash
#
# Report what this domain pins, and what k3s names as stable now.
#
# The newest k3s tag is the wrong question. k3s builds one release
# per supported Kubernetes minor, so the highest tag is whatever minor
# came out most recently, and it is often a version that nobody should
# run yet. The project answers the right question itself: it publishes
# a channel server, and the stable channel names the release it
# recommends. That is the field this script reads.
#
# A minor bump is a Kubernetes minor bump. Read the upstream release
# notes before you take one: it can remove an API version that a
# workload still uses.
#
# This pin carries no digest. Each k3s release publishes a sha256
# manifest per architecture, and k3s/fetch.sh reads it on every fetch,
# so a bump here is one line in VERSION.
#
# Usage:
#   k3s/latest.sh          report the pin and the stable channel
#   k3s/latest.sh --bump   write the stable release into VERSION

set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

channels="https://update.k3s.io/v1-release/channels"

pinned="$(cat "$here/VERSION")"

# The channel list is one JSON document with no newlines. Each
# channel's record starts with its id and ends with its version, so
# the first "latest" field after "id":"stable" is the stable
# channel's. This keeps the script's tool list at curl, with no JSON
# parser to install.
latest="$(curl -fsS --retry 3 "$channels" |
    sed 's/.*"id":"stable"//' |
    grep -o '"latest":"[^"]*"' |
    head -1 |
    cut -d'"' -f4)" || latest=""

printf '%s\t%s\t%s\t%s\n' k3s "$pinned" "${latest:-?}" \
    "the stable channel, not the newest tag"

[[ "${1:-}" == "--bump" ]] || exit 0

[[ -n "$latest" ]] || {
    echo "latest.sh: the channel server did not answer" >&2
    exit 1
}
[[ "$latest" != "$pinned" ]] || {
    echo "k3s $pinned is current"
    exit 0
}

echo "$latest" >"$here/VERSION"
echo "k3s: $pinned -> $latest"
echo "the release's sha256 manifest stands behind these bytes"
if [[ "${pinned%%+*}" != "${latest%%+*}" ]] &&
    [[ "$(cut -d. -f2 <<<"$pinned")" != "$(cut -d. -f2 <<<"$latest")" ]]; then
    echo "this crosses a Kubernetes minor; read the upstream release notes"
fi
echo "next: make -C .. k3s"
