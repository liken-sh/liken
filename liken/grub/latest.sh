#!/usr/bin/env bash
#
# Report what this domain pins, and what Ubuntu's development
# series carries now.
#
# The pin is a version of Ubuntu's `grub2` source package, and the bytes
# are Ubuntu's prebuilt `grub-pc-bin` deb from the archive's pool.
# Ubuntu publishes each package in several series at once, and each
# series carries its own upstream GRUB. Under the rule in root plan 83,
# a pin from a package repository follows the newest series that the
# repository offers to install whole, and for Ubuntu that is the
# development series. So the channel column is the series that publishes
# the pinned version, and the newest-channel column is the development
# series that Launchpad names as current. The latest column is the
# version in the development series, and a bare `--bump` moves the pin
# to it, across series when it must. The note names the newest build in
# the pin's own series, which is the security update to take while the
# pin stays on that series.
#
# Launchpad's API answers both questions, through the Launchpad
# functions of toolchains/pin.sh.
#
# The pool is the one upstream here that forgets. It keeps only the
# current build of each series, so a superseded pin returns a 404
# error and the domain stops building. A pin that is no longer in the
# pool's listing is reported as gone.
#
# Ubuntu signs no package. It signs each suite's InRelease file, which
# lists the digest of each Packages index, and each index lists the
# digest of each package. So a bump takes its digest through that
# chain with ubuntu_deb_digest in toolchains/pin.sh, against the
# archive key in trust/keys/ubuntu-archive.asc, and pins the sha256 of
# the bytes it verified.
#
# This domain fetches two debs and pins both. They always share a
# version, because they come from one source package, so one bump
# writes both digests.
#
# Usage:
#   grub/latest.sh                       report the pin
#   grub/latest.sh --bump                take the version in the
#                                        development series
#   grub/latest.sh --bump 2.12-1ubuntu7.3
#                                        take another version that the
#                                        pool carries

set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source-path=SCRIPTDIR source=../../toolchains/pin.sh
source "$here/../../toolchains/pin.sh"

pool="http://archive.ubuntu.com/ubuntu/pool/main/g/grub2"

pinned="$(cat "$here/VERSION")"

pinned_series="$(launchpad_published grub2 --data "version=$pinned" | head -1 | cut -d' ' -f2)" ||
    pinned_series=""
newest_series="$(ubuntu_development_series)" || newest_series=""

latest=""
[[ -z "$newest_series" ]] || latest="$(ubuntu_newest_in grub2 "$newest_series")" || latest=""
in_series=""
[[ -z "$pinned_series" ]] || in_series="$(ubuntu_newest_in grub2 "$pinned_series")" || in_series=""

versions="$(fetch_text "$pool/" |
    grep -oE 'grub-pc-bin_[^"_]+_amd64\.deb' |
    sed -e 's|^grub-pc-bin_||' -e 's|_amd64\.deb$||' |
    sort -uV)" || versions=""

note="${pinned_series:-its series} has ${in_series:-?}"
if [[ -n "$versions" ]] && ! grep -qxF "$pinned" <<<"$versions"; then
    note="the pin is gone from the pool"
fi

row grub "$pinned" "$latest" \
    "${pinned_series:-?}" "${newest_series:-?}" - "$note"

[[ "${1:-}" == "--bump" ]] || exit 0

[[ -n "$versions" ]] || fail "the pool listing did not answer"
[[ -n "${2:-}" || -n "$latest" ]] || fail "Launchpad named no version in the development series"

target="${2:-$latest}"
grep -qxF "$target" <<<"$versions" || fail "the pool does not carry $target"
bump_needed grub "$pinned" "$target" || exit 0

pc_bin="$(ubuntu_deb_digest grub2 grub-pc-bin main "$target")"
# grub-mkimage is in grub2-common from the 2.14 packaging on.
common="$(ubuntu_deb_digest grub2 grub2-common main "$target")"

echo "$target" >"$here/VERSION"
set_pin grub-pc-bin "$target" "$pool/grub-pc-bin_${target}_amd64.deb" "$pc_bin" \
    "$(deb_purl ubuntu grub-pc-bin "$target")"
set_pin grub2-common "$target" "$pool/grub2-common_${target}_amd64.deb" "$common" \
    "$(deb_purl ubuntu grub2-common "$target")"
bumped grub "$pinned" "$target"
echo "the digests came from the Packages index, checked against InRelease, signed by Ubuntu's archive key"
repin_sources
next_step "make -C .. grub"
