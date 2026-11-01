#!/usr/bin/env bash
#
# Build the static programs that every liken image carries: mke2fs,
# mount.nfs, wpa_supplicant, and the compiled time zone database.
#
# A liken machine has no loader and no shared libraries, so each
# program must be one static binary, and no project publishes static
# builds of these. buildroot builds them. It compiles a musl
# cross-compiler from source, then each library and each program from
# a source tarball that a .hash file pins, and it records the license
# and the source of each package that it built. The configuration is
# the BR2_EXTERNAL tree in this directory: defconfig selects buildroot's
# own recipes for the programs, external.mk and versions.mk hold what
# liken changes in them, and patches/ holds the .hash file of each
# version that versions.mk sets.
#
# The build runs in the `buildroot` image of toolchains/images.toml,
# which is buildroot's own CI image, so the host tools that buildroot
# starts from are pinned by digest too. The build needs docker or podman.
#
# Results land in userland/dist/:
#
#   sbin/mke2fs, sbin/mount.nfs,  the programs, each checked static
#   sbin/wpa_supplicant
#   zoneinfo/                     the time zone database
#   legal-info/                   buildroot's `make legal-info`: the
#                                 license files and the source tarballs
#                                 of every package in the build, and
#                                 manifest.csv, which names each one
#   sbom.cdx.json                 the CycloneDX SBOM that buildroot's
#                                 utils/generate-cyclonedx writes
#   versions                      one line for each program, its name
#                                 in the OS's components record and the
#                                 version that the build used
#
# The download directory is userland/cache/dl/, and the compiler cache
# is userland/cache/ccache/. Each build starts from a fresh buildroot
# tree in userland/_build/, and the compiler cache keeps a rebuild short.

set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source-path=SCRIPTDIR source=../../toolchains/pin.sh
source "$here/../../toolchains/pin.sh"

require_tools tar file

runtime="$(container_runtime)"
[[ -n "$runtime" ]] || fail "needs docker or podman to run the build container"
builder="$(image_of buildroot)"

"$here/fetch.sh"

commit="$(cat "$here/VERSION")"
# The work tree holds gcc's source, and gcc's test suite holds Go files
# that do not build. The go tool skips a directory whose name starts
# with an underscore, so `go vet ./...` in the liken module does not
# read them.
work="$here/_build"
out="$here/dist"

rm -rf "$work"
mkdir -p "$work" "$here/cache/dl" "$here/cache/ccache"
tar -xzf "$here/cache/buildroot/buildroot-$commit.tar.gz" -C "$work"
mv "$work/buildroot-$commit" "$work/buildroot"

# The container sees this directory read-only at /external, the
# buildroot tree and its output at /work, and the two caches. It runs
# as the calling user, so every file it writes belongs to that user.
# `make defconfig` expands defconfig into the full .config, and the
# plain `make` builds every package. `legal-info` then copies each
# package's license files and source tarballs out of the download
# directory, and `show-info` prints the JSON that generate-cyclonedx
# turns into the SBOM.
echo "building the static programs with buildroot ${commit:0:12}; the log is userland/_build/build.log"
"$runtime" run --rm -i \
    -u "$(id -u):$(id -g)" \
    -e HOME=/tmp \
    -e BR2_DL_DIR=/dl \
    -e BR2_CCACHE_DIR=/ccache \
    -v "$here:/external:ro" \
    -v "$work:/work" \
    -v "$here/cache/dl:/dl" \
    -v "$here/cache/ccache:/ccache" \
    "$builder" bash -e >"$work/build.log" 2>&1 <<'BUILD' || {
cd /work/buildroot
make O=/work/output BR2_EXTERNAL=/external BR2_DEFCONFIG=/external/defconfig defconfig
make O=/work/output
make O=/work/output legal-info
make --no-print-directory -s O=/work/output show-info | utils/generate-cyclonedx >/work/output/sbom.cdx.json
BUILD
    tail -40 "$work/build.log" >&2
    fail "the buildroot build failed; userland/_build/build.log holds the whole log"
}

output="$work/output"
rm -rf "$out"
mkdir -p "$out/sbin"

# Each program comes out of buildroot's target tree. A dynamically
# linked binary would run in the build container and fail on the
# machine, which has no loader, so this step refuses one. A static PIE
# passes too: it carries no loader request and no library, and
# relocates itself at start, and file(1) calls it "static-pie linked".
# buildroot strips each target binary already.
for program in mke2fs mount.nfs wpa_supplicant; do
    binary=""
    for dir in sbin usr/sbin usr/bin; do
        if [[ -f "$output/target/$dir/$program" && ! -L "$output/target/$dir/$program" ]]; then
            binary="$output/target/$dir/$program"
        fi
    done
    [[ -n "$binary" ]] || fail "buildroot's target tree holds no $program"
    file "$binary" | grep -qE "statically linked|static-pie linked" ||
        fail "$program is not statically linked: $(file -b "$binary")"
    install -m 0755 "$binary" "$out/sbin/$program"
done

# zic writes two trees of zones: posix/, whose clocks ignore leap
# seconds, and right/, whose clocks count them. Nothing in Kubernetes
# asks for right/, so the image takes the posix tree at the top of
# /usr/share/zoneinfo, where Go's time.LoadLocation looks for a name
# such as America/New_York, and the .tab files that list the zones.
# buildroot's recipe runs zic with -b fat, which writes each zone's
# transitions out explicitly. A slim file leaves future transitions to
# a rule string, and a glibc older than 2.34 in a workload that
# bind-mounts the host's /usr/share/zoneinfo misreads one.
mkdir -p "$out/zoneinfo"
cp -a "$output/host/share/zoneinfo/posix/." "$out/zoneinfo/"
cp -a "$output/host/share/zoneinfo/"*.tab "$out/zoneinfo/"

# A zone that zic did not write fails quietly: LoadLocation returns
# "unknown time zone" for that name, and the apiserver rejects the
# CronJob. These four cover a whole-hour offset, a half-hour offset, a
# half-hour offset with summer time, and a zone whose winter offset is
# zero.
for zone in UTC America/New_York Asia/Kolkata Australia/Lord_Howe Europe/Dublin; do
    [[ -f "$out/zoneinfo/$zone" ]] || fail "zic wrote no $zone"
done

# legal-info's sources/ holds the source of each package whose code
# the programs carry. host-sources/ holds the source of each tool that
# ran on the build machine, the compiler among them, which no machine
# receives, so dist/ leaves it out, except zic: its tzcode tarball is
# the program that compiled the zone files, and the source mirror
# offers it beside tzdata.
mkdir -p "$out/legal-info/host-sources"
for entry in "$output/legal-info"/*; do
    [[ "$(basename "$entry")" == host-sources ]] || cp -a "$entry" "$out/legal-info/"
done
cp -a "$output/legal-info/host-sources/"zic-* "$out/legal-info/host-sources/"
cp "$output/sbom.cdx.json" "$out/sbom.cdx.json"

# The versions file names each program by its name in the OS's
# components record, with the version that manifest.csv records for
# its buildroot package, so the image and the release document name
# the version that the build used.
version_of() {
    awk -F'","' -v p="$1" 'substr($1, 2) == p { print $2 }' "$out/legal-info/manifest.csv"
}
{
    for pair in e2fsprogs:e2fsprogs nfs-utils:nfs-utils wpa-supplicant:wpa_supplicant tzdata:tzdata; do
        version="$(version_of "${pair#*:}")"
        [[ -n "$version" ]] || fail "legal-info/manifest.csv names no ${pair#*:}"
        echo "${pair%%:*} $version"
    done
} >"$out/versions.partial"
mv "$out/versions.partial" "$out/versions"

# The build asks wpa_supplicant for its own version, because a tarball
# that unpacked to another release would otherwise ship silently.
"$out/sbin/wpa_supplicant" -v | grep -q "wpa_supplicant v$(version_of wpa_supplicant)" ||
    fail "wpa_supplicant does not report v$(version_of wpa_supplicant)"

echo "static programs (buildroot ${commit:0:12}):"
sed 's/^/  /' "$out/versions"
(cd "$out/sbin" && sha256sum mke2fs mount.nfs wpa_supplicant | sed 's/^/  /')
