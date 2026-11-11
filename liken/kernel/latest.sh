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
# file beside its packages, and --bump copies the two packages' file
# names and sha256 digests from it into kernel/pin.toml. A fetch reads
# only pin.toml, so a package that changes upstream after the bump
# fails the check.
#
# The digests are worth only as much as the file they come from, so
# --bump first checks CHECKSUMS.gpg, the archive's detached signature
# of the file, against the key in kernel-ppa.asc. The bump stops when
# the signature is missing, does not verify, or was made by any other
# key. The key belongs to Launchpad's "Ubuntu Kernel PPA" team
# (https://launchpad.net/~kernel-ppa), which lists it among the
# team's OpenPGP keys, and Canonical's mainline builds sign with it.
# Its fingerprint is pinned below as `signer`. A new key is a change
# that a person makes by hand, after checking the team's page.
#
# The same bump pins kernel.org's tarball of the release, which
# licensing/sources.sh mirrors as the kernel's corresponding source.
# Its digest comes from kernel.org's sha256sums.asc, through
# trust/keys/kernel-org-digest.sh, which checks the file's signature
# first.
#
# Usage:
#   kernel/latest.sh          report the pin and the newest release
#   kernel/latest.sh --bump   write the newest release into VERSION,
#                             and the URLs and digests of its packages
#                             and its source into pin.toml

set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source-path=SCRIPTDIR source=../../toolchains/pin.sh
source "$here/../../toolchains/pin.sh"

index="https://kernel.ubuntu.com/mainline/"

pinned="$(cat "$here/VERSION")"

# The index is an Apache directory listing, one link per built tag.
# Sorting needs the trailing slash gone: '.' sorts before '/', so
# "7.1.2/" would come out below "7.1/" and the newest release would
# be whichever one happened to be shortest.
latest="$(fetch_text "$index" |
    grep -oE 'href="v[0-9][^"]*/"' |
    sed -e 's|href="v||' -e 's|/"$||' |
    grep -vE 'rc' |
    sort -V | tail -1)" || latest=""

row kernel "$pinned" "$latest" mainline mainline - \
    "Canonical's mainline builds, which lag kernel.org"

[[ "${1:-}" == "--bump" ]] || exit 0

bump_needed kernel "$pinned" "$latest" || exit 0

signer="60AA7B6F30434AE68E569963E50C6A0917C622B0"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

# Not every release builds successfully, so a release in the index can
# have no build to pin.
url_exists "${index}v$latest/amd64/CHECKSUMS" ||
    fail "no mainline build found for $latest; check $index"
fetch_file "${index}v$latest/amd64/CHECKSUMS" "$work/CHECKSUMS"
fetch_file "${index}v$latest/amd64/CHECKSUMS.gpg" "$work/CHECKSUMS.gpg"
verify_signature --signer "$signer" "$here/kernel-ppa.asc" "$work/CHECKSUMS.gpg" "$work/CHECKSUMS"

# The CHECKSUMS file holds sha1 lines (40 hex characters) and then
# sha256 lines (64 characters), each in the form "<digest>  <filename>".
# Matching 64 characters keeps the sha256 lines. The filenames embed a
# build timestamp, so the pattern matches the package name around it.
checksums="$(cat "$work/CHECKSUMS")"
image="$(grep -E '^[0-9a-f]{64}  linux-image-unsigned-.*-generic_.*_amd64\.deb$' <<<"$checksums" | head -n1)" || image=""
modules="$(grep -E '^[0-9a-f]{64}  linux-modules-.*-generic_.*_amd64\.deb$' <<<"$checksums" | head -n1)" || modules=""
[[ -n "$image" && -n "$modules" ]] ||
    fail "the CHECKSUMS file of $latest lists no image or no modules package"

# pin_package <entry> <"<sha256>  <file name>"> [<cpe>]: write one
# package of the CHECKSUMS file into its entry of pin.toml.
pin_package() {
    local digest="${2%% *}" url="${index}v$latest/amd64/${2##* }"
    set_pin "$1" "$latest" "$url" "$digest" "$(generic_purl "$1" "$latest" "$url" "$digest")" "${3:-}"
}

# licensing/sources.sh mirrors kernel.org's tarball of the release as
# the kernel's corresponding source, from the linux-source entry of
# pin.toml. kernel.org lists the digest of each tarball of a major
# version in one sums file, signed by its checksum autosigner.
source_dir="https://cdn.kernel.org/pub/linux/kernel/v${latest%%.*}.x"
source_url="$source_dir/linux-$latest.tar.xz"
source_digest="$("$here/../../trust/keys/kernel-org-digest.sh" \
    "$source_dir/sha256sums.asc" "linux-$latest.tar.xz")"

echo "$latest" >"$here/VERSION"
pin_package linux-image-unsigned "$image" "cpe:2.3:o:linux:linux_kernel:$latest:*:*:*:*:*:*:*"
pin_package linux-modules "$modules"
set_pin linux-source "$latest" "$source_url" "$source_digest" \
    "$(generic_purl linux "$latest" "$source_url" "$source_digest")"
bumped kernel "$pinned" "$latest"
echo "the digests of the packages came from the build's CHECKSUMS file, signed by the Kernel PPA key"
echo "the digest of the source came from sha256sums.asc, signed by kernel.org's checksum autosigner"
next_step "make -C .. kernel"
