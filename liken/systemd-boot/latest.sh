#!/usr/bin/env bash
#
# Report what this domain pins, and what Ubuntu's development
# series carries now.
#
# The pin is a version of Ubuntu's `systemd` source package, and the
# bytes are Ubuntu's prebuilt `systemd-boot-efi` deb from the archive's
# pool. Ubuntu publishes each package in several series at once, and
# each series carries its own upstream systemd. Under the rule in root
# plan 83, a pin from a package repository follows the newest series
# that the repository offers to install whole, and for Ubuntu that is
# the development series. So the channel column is the series that
# publishes the pinned version, and the newest-channel column is the
# development series that Launchpad names as current. The latest column
# is the version in the development series, and a bare `--bump` moves
# the pin to it, across series when it must. The note names the newest
# build in the pin's own series, which is the security update to take
# while the pin stays on that series.
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
# The same bump pins the three files of the systemd source package,
# which licensing/sources.sh mirrors as the corresponding source. The
# suite's Sources index lists their digests, and InRelease lists the
# digest of the index, so ubuntu_source_files takes them through the
# same chain.
#
# Usage:
#   systemd-boot/latest.sh                     report the pin
#   systemd-boot/latest.sh --bump              take the version in the
#                                              development series
#   systemd-boot/latest.sh --bump 259.5-0ubuntu3.4
#                                              take another version that
#                                              the pool carries

set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source-path=SCRIPTDIR source=../../toolchains/pin.sh
source "$here/../../toolchains/pin.sh"

pool="http://archive.ubuntu.com/ubuntu/pool/universe/s/systemd"

pinned="$(cat "$here/VERSION")"

pinned_series="$(launchpad_published systemd --data "version=$pinned" | head -1 | cut -d' ' -f2)" ||
    pinned_series=""
newest_series="$(ubuntu_development_series)" || newest_series=""

latest=""
[[ -z "$newest_series" ]] || latest="$(ubuntu_newest_in systemd "$newest_series")" || latest=""
in_series=""
[[ -z "$pinned_series" ]] || in_series="$(ubuntu_newest_in systemd "$pinned_series")" || in_series=""

versions="$(fetch_text "$pool/" |
    grep -oE 'systemd-boot-efi_[^"_]+_amd64\.deb' |
    sed -e 's|^systemd-boot-efi_||' -e 's|_amd64\.deb$||' |
    sort -uV)" || versions=""

note="${pinned_series:-its series} has ${in_series:-?}"
if [[ -n "$versions" ]] && ! grep -qxF "$pinned" <<<"$versions"; then
    note="the pin is gone from the pool"
fi

row systemd-boot "$pinned" "$latest" \
    "${pinned_series:-?}" "${newest_series:-?}" - "$note"

[[ "${1:-}" == "--bump" ]] || exit 0

[[ -n "$versions" ]] || fail "the pool listing did not answer"
[[ -n "${2:-}" || -n "$latest" ]] || fail "Launchpad named no version in the development series"

target="${2:-$latest}"
grep -qxF "$target" <<<"$versions" || fail "the pool does not carry $target"
bump_needed systemd-boot "$pinned" "$target" || exit 0

digest="$(ubuntu_deb_digest systemd systemd-boot-efi universe "$target")"
# The systemd source package is in main, and the binary package is in
# universe.
sources="$(ubuntu_source_files systemd main "$target")"

echo "$target" >"$here/VERSION"
set_pin systemd-boot-efi "$target" "$pool/systemd-boot-efi_${target}_amd64.deb" "$digest" \
    "$(deb_purl ubuntu systemd-boot-efi "$target")"
set_ubuntu_source_pins systemd-source systemd "$target" "$sources"
bumped systemd-boot "$pinned" "$target"
echo "the digests came from the Packages and Sources indexes, checked against InRelease, signed by Ubuntu's archive key"
next_step "make -C .. systemd-boot"
