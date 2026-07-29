#!/usr/bin/env bash
#
# Report what this domain pins, and what the gokrazy recipe builds
# now.
#
# The upstream release is the wrong question here. liken does not
# build mke2fs: it vendors the static binary that the gokrazy project
# builds, so the version that matters is the one that project's
# recipe names, and not the newest tarball that the e2fsprogs
# maintainers have published. The recipe names it in a directory path,
# which is why this script reads a Makefile to learn a version.
#
# Two pins move together: the commit, which is what the fetch
# addresses, and the version, which is part of the path inside that
# commit. Both come from the same read of the recipe.
#
# Nothing upstream checksums that binary, so a bump computes the
# digest from its own download.
#
# Usage:
#   e2fsprogs/latest.sh          report the pins and the recipe's state
#   e2fsprogs/latest.sh --bump   write the commit, version, and digest

set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

repository="https://github.com/gokrazy/mkfs"
raw="https://raw.githubusercontent.com/gokrazy/mkfs"

pinned="$(cat "$here/VERSION")"
pinned_commit="$(sed -n 's|^commit="\(.*\)"$|\1|p' "$here/fetch.sh")"

head_commit="$(git ls-remote "$repository" HEAD | cut -f1)" || head_commit=""
recipe="$(curl -fsS --retry 3 "$raw/HEAD/Makefile" |
    grep -oE 'third_party/e2fsprogs-[0-9.]+' |
    head -1 |
    sed 's|.*e2fsprogs-||')" || recipe=""

printf '%s\t%s\t%s\t%s\n' e2fsprogs "$pinned" "${recipe:-?}" \
    "what the gokrazy recipe builds, not the newest release"
printf '%s\t%s\t%s\t%s\n' '  gokrazy' \
    "${pinned_commit:0:12}" "${head_commit:0:12}" \
    "the commit the mke2fs binary is fetched from"

[[ "${1:-}" == "--bump" ]] || exit 0

[[ -n "$head_commit" && -n "$recipe" ]] || {
    echo "latest.sh: the gokrazy repository did not answer" >&2
    exit 1
}
[[ "$head_commit" != "$pinned_commit" || "$recipe" != "$pinned" ]] || {
    echo "e2fsprogs $pinned at $pinned_commit is current"
    exit 0
}

url="$raw/$head_commit/third_party/e2fsprogs-$recipe/mke2fs.amd64"
digest="$(curl -fsSL --retry 3 "$url" | sha256sum | cut -d' ' -f1)"

echo "$recipe" >"$here/VERSION"
sed -i \
    -e "s|^commit=\".*\"|commit=\"$head_commit\"|" \
    -e "s|^digest=\".*\"|digest=\"$digest\"|" \
    "$here/fetch.sh"
echo "e2fsprogs: $pinned at ${pinned_commit:0:12} -> $recipe at ${head_commit:0:12}"
echo "the digest came from this download; nothing upstream signs it"
# A refused re-pin leaves the domain pin moved and the mirror
# behind it, which is a release that cannot serve its own
# sources. Say that plainly, because the fix is a hand edit.
"$here/../licensing/sources.sh" --repin || {
    echo "the pin moved; licensing/sources.sh still needs a hand edit" >&2
    exit 1
}
echo "next: make -C .. e2fsprogs"
