#!/usr/bin/env bash
#
# Report what this domain pins, and what Canonical's mainline archive
# has now.
#
# The pin is a kernel release, and the bytes are Canonical's mainline
# build of it: unmodified upstream source, packaged as debs. The
# channel is `mainline`, the one archive this domain reads, so both
# channel columns print it. Under the rule in root plan 83, a pin
# from a repository follows the newest release in that repository.
# Canonical builds each release some days after kernel.org tags it,
# so this pin lags kernel.org, and the rule accepts that lag until
# the kernel builds from source.
#
# The archive builds every upstream tag, and that includes release
# candidates: v7.2-rc5 sits in the same index as v7.1.5. liken boots
# releases, so this script drops every name with an -rc suffix and
# takes the highest of what is left.
#
# Not every tag in that index built successfully, and the index lists
# the directory either way. So a bump can name a version whose
# packages are missing, and --bump says so: it stops when the build
# has no CHECKSUMS file or the file lists no image or modules package.
#
# The pin carries a digest for each of the two packages that
# kernel/fetch.sh unpacks. Each mainline build publishes a CHECKSUMS
# file beside its packages, and --bump copies the two packages' sha256
# lines from it into fetch.sh. A fetch reads only those lines, so a
# package that changes upstream after the bump fails the check.
#
# Usage:
#   kernel/latest.sh          report the pin and the newest release
#   kernel/latest.sh --bump   write the newest release into VERSION,
#                             and its packages' digests into fetch.sh

set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

index="https://kernel.ubuntu.com/mainline/"

pinned="$(cat "$here/VERSION")"

# The index is an Apache directory listing, one link per built tag.
# Sorting needs the trailing slash gone: '.' sorts before '/', so
# "7.1.2/" would come out below "7.1/" and the newest release would
# be whichever one happened to be shortest.
latest="$(curl -fsS --retry 3 --max-time 60 "$index" |
    grep -oE 'href="v[0-9][^"]*/"' |
    sed -e 's|href="v||' -e 's|/"$||' |
    grep -vE 'rc' |
    sort -V | tail -1)" || latest=""

printf '%s\t%s\t%s\t%s\t%s\t%s\t%s\n' kernel "$pinned" "${latest:-?}" \
    mainline mainline - \
    "Canonical's mainline builds, which lag kernel.org"

[[ "${1:-}" == "--bump" ]] || exit 0

[[ -n "$latest" ]] || {
    echo "latest.sh: the mainline index did not answer" >&2
    exit 1
}
[[ "$latest" != "$pinned" ]] || {
    echo "kernel $pinned is current"
    exit 0
}

# The CHECKSUMS file holds sha1 lines (40 hex characters) and then
# sha256 lines (64 characters), each in the form "<digest>  <filename>".
# Matching 64 characters keeps the sha256 lines. The filenames embed a
# build timestamp, so the pattern matches the package name around it.
checksums="$(curl -fsSL --retry 3 --max-time 60 "${index}v$latest/amd64/CHECKSUMS")" || {
    echo "latest.sh: no mainline build found for $latest" >&2
    echo "latest.sh: (not every release builds successfully; check $index)" >&2
    exit 1
}
image="$(grep -E '^[0-9a-f]{64}  linux-image-unsigned-.*-generic_.*_amd64\.deb$' <<<"$checksums" | head -n1)" || image=""
modules="$(grep -E '^[0-9a-f]{64}  linux-modules-.*-generic_.*_amd64\.deb$' <<<"$checksums" | head -n1)" || modules=""
[[ -n "$image" && -n "$modules" ]] || {
    echo "latest.sh: the CHECKSUMS file of $latest lists no image or no modules package" >&2
    exit 1
}

# Each array in fetch.sh holds one entry, the pinned version's, so the
# bump replaces the line after each array's opening line.
echo "$latest" >"$here/VERSION"
sed -i \
    -e "/^declare -A image_package=(/{n;s|.*|    [\"$latest\"]=\"$image\"|}" \
    -e "/^declare -A modules_package=(/{n;s|.*|    [\"$latest\"]=\"$modules\"|}" \
    "$here/fetch.sh"
echo "kernel: $pinned -> $latest"
echo "the digests came from the build's CHECKSUMS file; its GPG signature is not checked"
# licensing/sources.sh mirrors kernel.org's tarball of this release
# and pins it by digest, so a kernel bump moves that digest too. A
# refused re-pin leaves the domain pin moved and the mirror behind
# it, which is a release that cannot serve its own sources. Say that
# plainly, because the fix is a hand edit.
"$here/../licensing/sources.sh" --repin || {
    echo "the pin moved; licensing/sources.sh still needs a hand edit" >&2
    exit 1
}
echo "next: make -C .. kernel"
