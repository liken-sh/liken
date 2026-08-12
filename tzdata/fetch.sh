#!/usr/bin/env bash
#
# Vendor the timezone database: the compiled rules that turn
# "America/New_York" into an offset for a given instant.
#
# Kubernetes needs this on the host. A CronJob names its zone in
# spec.timeZone, and two halves of that feature resolve the name
# through Go's time.LoadLocation: the apiserver, which validates the
# CronJob at admission, and the controller, which schedules it. Both
# run inside the k3s process. LoadLocation reads
# $ZONEINFO, then /usr/share/zoneinfo, then two paths no modern
# system uses, then the copy compiled into the binary when the
# program imported time/tzdata. k3s does not import it. So the
# database must be a directory on the machine, and staging it at the
# conventional path means no program needs to be told where it is.
#
# IANA publishes rules as text, not as the binary TZif files a
# program reads, so someone must run zic. This script runs it, from
# IANA's own source, rather than copying a compiled tree out of a
# distribution package. Three reasons:
#
#   The version matches the release IANA announces. When a country
#   moves its clocks and the fix ships as 2026c, tzdata/VERSION says
#   2026c, not 2026c-0ubuntu0.24.04 and not 2026.3.
#
#   The archive does not rot. data.iana.org keeps every release back
#   to 1993. The Ubuntu pool returns a 404 error the day a newer
#   version replaces the pinned one, as grub/fetch.sh and
#   systemd-boot/fetch.sh both record, and tzdata is superseded four
#   to six times a year.
#
#   liken picks the zic flags. See the REDO and ZFLAGS note below.
#
# Unlike every other vendored domain, this one verifies a signature
# before it verifies a digest. A digest pin detects a change made
# after somebody chose the version. It cannot detect a bad download
# at the moment the pin was written, because the person who wrote the
# pin computed it from that same download. tzdata/latest.sh --bump
# moves this pin, and it writes the digest from bytes it fetched
# itself, so no person sees them before the digest is written. IANA
# signs each release, and tz.asc holds the coordinator's public key.
#
# The digest stays, in DIGESTS, and does its usual job: a rebuild
# produces the same bytes the pull request showed. It lives in its
# own file rather than in a variable here, so that moving the pin is
# a two-file diff of four lines that a robot can write without
# editing a shell script.
#
# Usage:
#   tzdata/fetch.sh                   build the version pinned in
#                                     tzdata/VERSION
#   tzdata/fetch.sh --sources-only    fetch and verify the source
#                                     tarballs, skipping the build
#
# --sources-only exists for the licensing domain, which mirrors these
# same tarballs to the release channel. Mirroring needs the verified
# bytes, not the build, and it has no container runtime.
#
# Results land in tzdata/dist/<version>/zoneinfo/, with the source
# tarballs cached in tzdata/cache/.

set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

sources_only=""
[[ "${1:-}" != "--sources-only" ]] || sources_only=1

for tool in curl sha256sum gpg gpgv; do
    command -v "$tool" >/dev/null || {
        echo "fetch.sh: missing required tool: $tool" >&2
        exit 1
    }
done

runtime=""
for candidate in docker podman; do
    if command -v "$candidate" >/dev/null; then
        runtime="$candidate"
        break
    fi
done
[[ -n "$runtime" || -n "$sources_only" ]] || {
    echo "fetch.sh: needs docker or podman to run the pinned build container" >&2
    exit 1
}

version="$(cat "$here/VERSION")"

# The builder is pinned by image digest, the same as the other two
# domains that compile from source. zic is a few thousand lines of C
# with no dependency beyond libc, and its output is architecture
# independent data, so the toolchain has little effect on the result.
builder="docker.io/library/alpine@sha256:14358309a308569c32bdc37e2e0e9694be33a9d99e68afb0f5ff33cc1f695dce" # 3.22

releases="https://data.iana.org/time-zones/releases"

cache="$here/cache/$version"
out="$here/dist/$version"
mkdir -p "$cache"

# gpgv reads a binary keyring, and the repository carries the key
# armored, because a diff can show armored text. Dearmoring into the
# cache keeps the working tree clean.
gpg --dearmor <"$here/tz.asc" >"$cache/tz.gpg.partial"
mv "$cache/tz.gpg.partial" "$cache/tz.gpg"

# Download once, verify every time. A cached tarball that stops
# matching its signature or its digest fails the build.
for name in "tzcode$version.tar.gz" "tzdata$version.tar.gz"; do
    for file in "$name" "$name.asc"; do
        if [[ ! -f "$cache/$file" ]]; then
            curl -fsSL --retry 5 --retry-all-errors --retry-delay 5 -C - \
                "$releases/$file" -o "$cache/$file.partial"
            mv "$cache/$file.partial" "$cache/$file"
        fi
    done
    gpgv --keyring "$cache/tz.gpg" "$cache/$name.asc" "$cache/$name" 2>&1 |
        sed 's/^/  /'
done

(cd "$cache" && sha256sum --check --quiet "$here/DIGESTS")

[[ -z "$sources_only" ]] || exit 0

rm -rf "$out"
mkdir -p "$out/zoneinfo"

# The build runs inside the pinned container: sources mounted
# read-only at /in, the output directory writable at /out.
#
# REDO=posix_only builds one tree of zones rather than three. The
# other two are the leap-second variants, right/ and posix/, whose
# clocks count leap seconds. Nothing in Kubernetes asks for one.
#
# ZFLAGS=-bfat writes each zone's transitions out explicitly. The
# alternative, -bslim, writes the smallest file that describes a zone
# and leaves future transitions to the POSIX rule string in the
# file's footer. Slim saves 33 KB in liken's squashfs. It also breaks
# a workload container that bind-mounts the host's
# /usr/share/zoneinfo and holds a glibc older than 2.34, which
# misreads a slim file.
#
# The install also writes zic, zdump, libtz.a, manual pages, and an
# /etc/localtime. Only the zone tree is copied out. liken's machines
# run on UTC: Go's time.Local falls back to UTC when /etc/localtime
# is absent, every timestamp liken writes is UTC, and a CronJob names
# its own zone rather than reading the machine's.
"$runtime" run --rm -i \
    -v "$cache:/in:ro" \
    -v "$out:/out" \
    -e VERSION="$version" \
    -e HOST_UID="$(id -u)" \
    -e HOST_GID="$(id -g)" \
    "$builder" sh -e <<'BUILD'
apk add --quiet build-base
mkdir /build
tar xzf "/in/tzcode$VERSION.tar.gz" -C /build
tar xzf "/in/tzdata$VERSION.tar.gz" -C /build
cd /build

# musl carries gettext in libc rather than in a separate libintl, so
# there is no -lintl to link. HAVE_GETTEXT=0 stops the tz makefile
# from asking for one.
make CFLAGS=-DHAVE_GETTEXT=0 TOPDIR=/build/root \
    REDO=posix_only ZFLAGS=-bfat install >/dev/null

cp -a /build/root/usr/share/zoneinfo/. /out/zoneinfo/
chown -R "$HOST_UID:$HOST_GID" /out
BUILD

# A zone that zic did not write would fail quietly: the tree would
# exist, LoadLocation would return "unknown time zone" for that name,
# and the apiserver would reject the CronJob exactly as it did before
# this domain existed. These four cover a whole-hour offset, a
# half-hour offset, a half-hour offset with summer time, and a zone
# whose winter offset is zero.
for zone in America/New_York Asia/Kolkata Australia/Lord_Howe Europe/Dublin; do
    [[ -f "$out/zoneinfo/$zone" ]] || {
        echo "fetch.sh: zic wrote no $zone" >&2
        exit 1
    }
done

echo
echo "tzdata $version:"
find "$out/zoneinfo" -type f | wc -l | xargs -I{} echo "{} files"
