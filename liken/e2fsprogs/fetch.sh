#!/usr/bin/env bash
#
# Build mke2fs: the program that creates ext4 filesystems, statically
# linked, for init to run when it claims a blank disk.
#
# Making a filesystem is a userspace job. The kernel can read and
# write ext4 (liken's vendored kernel builds the driver in), but it has
# no code to create one. Laying down superblocks, block groups, inode
# tables, and the journal has always belonged to mke2fs, from the
# e2fsprogs package every distribution ships. liken's image has no
# libc and no shell, so a distro's dynamically-linked mke2fs cannot
# even run here. The binary must be static, like everything else on
# this machine.
#
# There is no pure-Go implementation to use instead. The strongest
# evidence comes from gokrazy, the appliance Linux whose userland is
# otherwise entirely Go: when gokrazy needed to format its permanent
# partition, it bundled a static mke2fs instead of rewriting ext4
# creation.
#
# No project publishes a static mke2fs for each e2fsprogs release, so
# this script builds one, the same way open-iscsi/fetch.sh and
# nfs-utils/fetch.sh build their binaries. It downloads the release
# tarball from kernel.org, checks it against the sha256sums.asc file
# that the e2fsprogs maintainer publishes in the same directory, and
# builds mke2fs in the `alpine` image that toolchains/images.toml
# names. Alpine's libc is musl, and Alpine ships musl's static
# archive, so the link takes everything into the one binary, and the
# binary runs on a machine with no libraries. e2fsprogs carries its
# own copies of libuuid and libblkid, and the build uses them, so musl
# is the only library from outside the tarball. The build needs docker
# or podman on the build host.
#
# Usage:
#   e2fsprogs/fetch.sh                  build the version pinned in
#                                       e2fsprogs/VERSION
#   e2fsprogs/fetch.sh --sources-only   fetch and verify the source
#                                       tarball, and skip the build
#
# --sources-only exists for the licensing domain: the release channel
# mirrors the same tarball as the source that corresponds to the
# binary, and that needs the verified bytes, not the build.
#
# Results land in e2fsprogs/dist/<version>/, with the tarball and its
# sums file cached in e2fsprogs/cache/<version>/.

set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

sources_only=""
[[ "${1:-}" != "--sources-only" ]] || sources_only=1

for tool in curl sha256sum; do
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
    echo "fetch.sh: needs docker or podman to run the build container" >&2
    exit 1
}

version="$(cat "$here/VERSION")"
tarball="e2fsprogs-$version.tar.gz"
release="https://www.kernel.org/pub/linux/kernel/people/tytso/e2fsprogs/v$version"

# The builder is the `alpine` entry in toolchains/images.toml, a tag
# and a digest. Each static build in the OS, and `bluetoothd`, builds
# in that image, and one entry keeps them all on one toolchain. The
# table names Docker Hub's short form, and podman needs the registry
# in full, so the read adds it.
builder="$(sed -n '/^\[build\]/,/^\[/ s|^alpine = "\(.*\)"$|docker.io/library/\1|p' \
    "$here/../../toolchains/images.toml")"
[[ -n "$builder" ]] || {
    echo "fetch.sh: toolchains/images.toml names no alpine image" >&2
    exit 1
}

cache="$here/cache/$version"
out="$here/dist/$version"
mkdir -p "$cache"

# Download once, verify every time. The sums file is the pin: it is
# the maintainer's list of digests for this release, so the version in
# VERSION is the only value this repository keeps. If a cached tarball
# stops matching the list, the build fails instead of using it.
download() {
    if [[ ! -f "$cache/$1" ]]; then
        curl -fsSL --retry 5 --retry-all-errors --retry-delay 5 --max-time 300 \
            "$release/$1" -o "$cache/$1.partial"
        mv "$cache/$1.partial" "$cache/$1"
    fi
}
download sha256sums.asc
download "$tarball"

# The sums file is a clearsigned document, so the digest lines sit
# among the signature's own text. Matching the exact filename picks
# the one line that matters.
digest="$(awk -v t="$tarball" '$2 == t { print $1 }' "$cache/sha256sums.asc")"
[[ -n "$digest" ]] || {
    echo "fetch.sh: $release/sha256sums.asc lists no $tarball" >&2
    exit 1
}
echo "$digest  $cache/$tarball" | sha256sum --check --quiet

[[ -z "$sources_only" ]] || exit 0

mkdir -p "$out"

# The build runs inside the builder: the tarball mounted read-only at
# /in, the dist directory writable at /out, and the script below piped
# to the container's shell. e2fsprogs builds its libraries first, and
# mke2fs links them as static archives. NLS stays off, because the
# machine has no message catalogs, and fuse2fs stays off, because it
# is a separate program that needs libfuse.
#
# musl declares lseek64 only as a macro for lseek, under
# _LARGEFILE64_SOURCE, and has no lseek64 symbol to link. configure
# tests for lseek64 with a link, so it reports the function missing,
# and llseek.c in libblkid then takes a fallback path that does not
# compile. The cache variable tells configure that lseek64 exists, and
# the macro turns each call into lseek, which takes a 64-bit offset on
# musl.
"$runtime" run --rm -i \
    -v "$cache:/in:ro" \
    -v "$out:/out" \
    -e VERSION="$version" \
    -e HOST_UID="$(id -u)" \
    -e HOST_GID="$(id -g)" \
    "$builder" sh -e <<'BUILD'
apk add --quiet build-base linux-headers file
mkdir /build
tar xzf "/in/e2fsprogs-$VERSION.tar.gz" -C /build
cd "/build/e2fsprogs-$VERSION"
./configure --quiet --disable-nls --disable-fuse2fs \
    ac_cv_func_lseek64=yes \
    CFLAGS='-O2 -D_LARGEFILE64_SOURCE' LDFLAGS='-static' >/dev/null
make -j"$(nproc)" libs >/dev/null
make -j"$(nproc)" -C misc mke2fs >/dev/null

# A dynamically linked binary here would run fine in this container
# and fail on the machine, which has no loader and no libraries. This
# step refuses to produce one.
strip misc/mke2fs
file misc/mke2fs | grep -q "statically linked" || {
    echo "fetch.sh: mke2fs is not statically linked" >&2
    exit 1
}
install -m 0755 misc/mke2fs /out/mke2fs
chown "$HOST_UID:$HOST_GID" /out/mke2fs
BUILD

echo "mke2fs (e2fsprogs $version):"
(cd "$out" && sha256sum mke2fs)
