#!/usr/bin/env bash
#
# Report the Debian snapshot that the bases and astrometry install
# from, and what the newest snapshot of sid holds.
#
# The five bases, vulkan, vaapi, ffmpeg, mpv, and weston, and the plate
# solver astrometry install every package from one day of
# snapshot.debian.org. Each package.toml states that day as its
# version, and lists under [package.upstream] the releases that the day
# holds. snapshot.sh in this directory points apt at the day, and
# snapshot.sha256 holds the checksum of the day's InRelease file.
#
# The channel is sid, which is the newest suite that Debian offers to
# install as a whole. experimental only adds packages on top of sid.
# Root plan 82 gives the reasons for sid.
#
# A snapshot date names the newest snapshot before midnight UTC of
# that day, so the files of a date stop changing at that midnight.
# Today's date in UTC is the newest date whose files are fixed, and it
# is the newest snapshot. The upstream rows read the Packages indexes
# of that snapshot, so each row shows what a bump to that date
# installs.
#
# The debian:sid-slim digest in toolchains/images.toml moves with the
# date, because the image and the snapshot must come from the same
# day. toolchains/latest.sh reports the digest, and the bump here
# writes it.
#
# Usage:
#   vulkan/latest.sh          report the date and every upstream release
#   vulkan/latest.sh --bump   move all six images to the newest date:
#                             the version, the revision, the upstream
#                             releases, snapshot.sha256, and the digest

set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source-path=SCRIPTDIR source=../toolchains/pin.sh
source "$here/../toolchains/pin.sh"
root="$(cd "$here/.." && pwd)"
images=(vulkan vaapi ffmpeg mpv weston astrometry)

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

pinned="$(sed -n 's/^version = "\([0-9]*\)"$/\1/p' "$here/package.toml")"
latest="$(date -u +%Y%m%d)"
archive="https://snapshot.debian.org/archive/debian/${latest}T000000Z"

# upstreams prints "image name version" for each [package.upstream]
# entry of the six images that the snapshot installs.
upstreams() {
    local image name version
    for image in "${images[@]}"; do
        awk -v i="$image" '/^\[/ { s = $0; next }
            s == "[package.upstream]" && / = / { gsub(/"/, "", $3); print i, $1, $3 }' \
            "$root/$image/package.toml"
    done | while read -r image name version; do
        built_from_source "$image" "$name" || echo "$image $name $version"
    done
}

# built_from_source is true for an entry that the image builds from an
# upstream release and not from the snapshot. The weston image builds
# Weston from its release tarball, and weston/latest.sh reports and
# bumps that entry. sid also has a weston package, so without this
# check a bump here writes the version of Debian's package over the
# release that the image builds.
built_from_source() {
    case "$1/$2" in
    weston/weston) return 0 ;;
    esac
    return 1
}

# The Packages indexes of the snapshot, as "package source
# source-version version". vaapi installs the Intel media driver from
# non-free, so the report reads that component too.
indexes() {
    local component
    for component in main non-free; do
        fetch_text "$archive/dists/sid/$component/binary-amd64/Packages.xz" --max-time 120 |
            xz -dc | awk '
                /^Package:/ { p = $2; s = $2; sv = "" }
                /^Source:/ { s = $2; if ($3 ~ /^\(/) { sv = $3; gsub(/[()]/, "", sv) } }
                /^Version:/ { v = $2 }
                /^$/ { print p, s, (sv == "" ? v : sv), v }'
    done
}

# A label key cannot hold a dot, so an entry names a Debian package
# with a dot in another form.
debian_name() {
    case "$1" in
    astrometry-net) echo astrometry.net ;;
    *) echo "$1" ;;
    esac
}

# The version of one entry in the snapshot. An entry that names a
# binary package, such as mpv, carries the binary's version, which
# includes a binNMU suffix such as +b4. An entry that names only a
# source package, such as mesa, carries the source version.
newest_version() {
    local name
    name="$(debian_name "$1")"
    awk -v n="$name" '$1 == n { print $4 }' "$work/packages" | sort -V | tail -1 | grep . ||
        awk -v n="$name" '$2 == n { print $3 }' "$work/packages" | sort -V | tail -1
}

if indexes >"$work/packages" 2>/dev/null && [[ -s "$work/packages" ]]; then
    have_indexes=1
else
    have_indexes=""
    latest=""
fi

row snapshot "$pinned" "$latest" sid sid - "the snapshot.debian.org date of all six images"
upstreams | awk '{ print $2, $3 }' | sort -u | awk '{ v[$1] = v[$1] (v[$1] ? "," : "") $2 } END { for (n in v) print n, v[n] }' |
    sort | while read -r name versions; do
    now=""
    [[ -z "$have_indexes" ]] || now="$(newest_version "$name")" || now=""
    row "  $name" "$versions" "$now" - - - "in $(upstreams | awk -v n="$name" '$2 == n { print $1 }' | paste -sd' ')"
done

[[ "${1:-}" == "--bump" ]] || exit 0

bump_needed "the snapshot" "$pinned" "$latest" || exit 0

# The checksum of the day's InRelease file binds the date to its
# package lists. snapshot.debian.org serves the file that Debian's
# archive signed, so the bump verifies that signature against Debian's
# archive keys in trust/keys/debian-archive.asc before it pins the
# digest of the same bytes. Without the check, a snapshot server or a
# mirror that changed the file would change the pin, and apt in the
# build would accept the file only because its digest matches.
fetch_file "$archive/dists/sid/InRelease" "$work/InRelease"
verify_signature "$root/trust/keys/debian-archive.asc" "$work/InRelease" >/dev/null
inrelease="$(sha256sum "$work/InRelease" | cut -d' ' -f1)"

# The digest of debian:sid-slim today. Each build image comes from the
# same day as the snapshot.
digest="$(registry_digest debian sid-slim)" || digest=""
[[ "$digest" =~ ^sha256:[0-9a-f]{64}$ ]] ||
    fail "Docker Hub did not answer with the digest of debian:sid-slim"

set_line "$here/snapshot.sha256" \
    '^[0-9a-f]+  snapshot\.debian\.org_archive_debian_[0-9]*T000000Z_dists_sid_InRelease$' \
    "$inrelease  snapshot.debian.org_archive_debian_${latest}T000000Z_dists_sid_InRelease"
set_toml "$root/toolchains/images.toml" build debian "\"debian:sid-slim@$digest\""

# A new date starts every revision at 1 again, so the bump needs no
# cascade of revisions up the graph.
for image in "${images[@]}"; do
    set_toml "$root/$image/package.toml" package version "\"$latest\""
    set_toml "$root/$image/package.toml" package revision 1
done
upstreams | while read -r image name old; do
    new="$(newest_version "$name")"
    set_toml "$root/$image/package.toml" package.upstream "$name" "\"$new\""
    [[ "$old" == "$new" ]] || bumped "$image" "$name $old" "$new"
done
bumped snapshot "$pinned" "$latest"
echo "the InRelease digest came from the snapshot's InRelease, signed by Debian's archive keys"
echo "debian:sid-slim: $digest"
next_step "make workflows and make images at the top of the repository, run each"
echo "image's smoke check, and read the release notes of each major release that moved"
