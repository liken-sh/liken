#!/usr/bin/env bash
#
# Report what this domain pins, and what Ubuntu's pool carries now.
#
# The pool is not a list of releases. It carries the current build of
# every series at once, so `2.12-1ubuntu7.3` and `2.14-2ubuntu3` sit
# in the same directory, and the newest name in it is a different
# upstream GRUB, not an update to this one. Moving between those is a
# decision, so this script reports two different things: the newest
# build in the pin's own series line, which is the security update you
# almost always want, and the newest build in the whole pool, in the
# note.
#
# The pool is also the one upstream here that forgets. It keeps only
# the current build of each series, so a superseded pin returns a 404
# error and the domain stops building. A pin that is no longer in the
# listing is reported as gone.
#
# This domain fetches two debs and pins both. They always share a
# version, because they come from one source package, so one bump
# writes both digests.
#
# Usage:
#   grub/latest.sh                       report the pin
#   grub/latest.sh --bump                take the newest build in the
#                                        pin's series
#   grub/latest.sh --bump 2.14-2ubuntu3  cross to another series by
#                                        naming the version

set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

pool="http://archive.ubuntu.com/ubuntu/pool/main/g/grub2"

pinned="$(cat "$here/VERSION")"

# The series line is the version up to its ubuntu revision. Ubuntu
# appends a further .N for each update inside a series, so
# 2.12-1ubuntu7.3 and 2.12-1ubuntu7 belong to one line, and
# 2.14-2ubuntu2 does not.
line="$(sed -E 's|^(.*ubuntu[0-9]+).*|\1|' <<<"$pinned")"

versions="$(curl -fsS --retry 3 "$pool/" |
    grep -oE 'grub-pc-bin_[^"_]+_amd64\.deb' |
    sed -e 's|^grub-pc-bin_||' -e 's|_amd64\.deb$||' |
    sort -uV)" || versions=""

latest="$(grep -E "^${line//./\\.}(\.[0-9]+)?$" <<<"$versions" | tail -1)" || latest=""
newest="$(tail -1 <<<"$versions")"

note="in its series line"
if [[ -n "$versions" ]] && ! grep -qxF "$pinned" <<<"$versions"; then
    note="the pin is gone from the pool"
elif [[ -n "$newest" && "$newest" != "$latest" ]]; then
    note="in its series line; the pool also has $newest"
fi

printf '%s\t%s\t%s\t%s\n' grub "$pinned" "${latest:-?}" "$note"

[[ "${1:-}" == "--bump" ]] || exit 0

[[ -n "$versions" ]] || {
    echo "latest.sh: the pool listing did not answer" >&2
    exit 1
}

target="${2:-$latest}"
grep -qxF "$target" <<<"$versions" || {
    echo "latest.sh: the pool does not carry $target" >&2
    exit 1
}
[[ "$target" != "$pinned" ]] || {
    echo "grub $pinned is current in its series line"
    exit 0
}

pc_bin="$(curl -fsSL --retry 3 "$pool/grub-pc-bin_${target}_amd64.deb" |
    sha256sum | cut -d' ' -f1)"
common="$(curl -fsSL --retry 3 "$pool/grub-common_${target}_amd64.deb" |
    sha256sum | cut -d' ' -f1)"

# The old entries stay. An older checkout still names its own version,
# and a digest table that forgets is a build that cannot be repeated.
echo "$target" >"$here/VERSION"
sed -i \
    -e "/^declare -A pc_bin_sha256=(/a\\    [\"$target\"]=\"$pc_bin\"" \
    -e "/^declare -A common_sha256=(/a\\    [\"$target\"]=\"$common\"" \
    "$here/fetch.sh"
echo "grub: $pinned -> $target"
echo "the digests came from these downloads; Ubuntu signs indexes, not files"
# A refused re-pin leaves the domain pin moved and the mirror
# behind it, which is a release that cannot serve its own
# sources. Say that plainly, because the fix is a hand edit.
"$here/../licensing/sources.sh" --repin || {
    echo "the pin moved; licensing/sources.sh still needs a hand edit" >&2
    exit 1
}
echo "next: make -C .. grub"
