#!/usr/bin/env bash
#
# Report what this domain pins, and what k3s names as latest now.
#
# The pin is a k3s release, and the bytes are the binary that the k3s
# project publishes on GitHub for it. The project runs a channel
# server, update.k3s.io, which names one release for each channel.
# This script reads the `latest` channel, because the rule in root
# plan 83 takes the newest channel that a channel server offers. The
# `stable` channel trails it by a Kubernetes minor. The newest tag is
# not the answer either: k3s builds one release for each supported
# Kubernetes minor, so the highest tag can be a patch to an older
# minor, and the `latest` channel names the newest release of the
# newest minor.
#
# The bump reads the same channel, so both channel columns print
# `latest`.
#
# A minor bump is a Kubernetes minor bump. Read the upstream release
# notes before you take one: it can remove an API version that a
# workload still uses.
#
# The pin carries the binary's digest in k3s/fetch.sh. Each k3s
# release publishes a sha256 manifest per architecture, and --bump
# copies the binary's line from it into fetch.sh.
#
# Usage:
#   k3s/latest.sh          report the pin and the latest channel
#   k3s/latest.sh --bump   write the latest channel's release into
#                          VERSION, and its digest into fetch.sh

set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

channels="https://update.k3s.io/v1-release/channels"

pinned="$(cat "$here/VERSION")"

# The channel list is one JSON document with no newlines. Each
# channel's record starts with its id and ends with its version, so
# the first "latest" field after "id":"latest" is the latest
# channel's. This keeps the script's tool list at curl, with no JSON
# parser to install.
latest="$(curl -fsS --retry 3 --max-time 60 "$channels" |
    sed 's/.*"id":"latest"//' |
    grep -o '"latest":"[^"]*"' |
    head -1 |
    cut -d'"' -f4)" || latest=""

printf '%s\t%s\t%s\t%s\t%s\t%s\t%s\n' k3s "$pinned" "${latest:-?}" \
    latest latest - "the release that update.k3s.io names for the latest channel"

[[ "${1:-}" == "--bump" ]] || exit 0

[[ -n "$latest" ]] || {
    echo "latest.sh: the channel server did not answer" >&2
    exit 1
}
[[ "$latest" != "$pinned" ]] || {
    echo "k3s $pinned is current"
    exit 0
}

# The manifest covers all of the release's artifacts. The line that
# ends in exactly " k3s" names the bare server and agent binary. Its
# siblings are airgap image bundles, which liken does not vendor; a
# machine pulls its images over the network on its first boot instead.
# The tag carries a +, which a URL path must escape.
manifest="https://github.com/k3s-io/k3s/releases/download/${latest//+/%2B}/sha256sum-amd64.txt"
digest="$(curl -fsSL --retry 3 --max-time 60 "$manifest" | awk '$2 == "k3s" { print $1 }')" || digest=""
[[ "$digest" =~ ^[0-9a-f]{64}$ ]] || {
    echo "latest.sh: $manifest lists no k3s binary" >&2
    exit 1
}

# The array in fetch.sh holds one entry, the pinned version's, so the
# bump replaces the line after the array's opening line.
echo "$latest" >"$here/VERSION"
sed -i "/^declare -A k3s_sha256=(/{n;s|.*|    [\"$latest\"]=\"$digest\"|}" "$here/fetch.sh"
echo "k3s: $pinned -> $latest"
echo "the digest came from the release's sha256 manifest"
if [[ "${pinned%%+*}" != "${latest%%+*}" ]] &&
    [[ "$(cut -d. -f2 <<<"$pinned")" != "$(cut -d. -f2 <<<"$latest")" ]]; then
    echo "this crosses a Kubernetes minor; read the upstream release notes"
fi
echo "next: make -C .. k3s"
