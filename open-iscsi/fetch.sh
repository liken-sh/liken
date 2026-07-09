#!/usr/bin/env bash
#
# Vendor the iSCSI initiator userspace: static iscsid and iscsiadm,
# the host half of the iscsi feature (see
# plans/17-network-storage-clients.md).
#
# CSI drivers that attach iSCSI volumes carry no initiator of their
# own. synology-csi, for one, mounts the host's root filesystem and
# chroots into it to exec whatever iscsiadm it finds there, and
# iscsiadm is in turn a client for a running iscsid daemon. So the OS
# must provide both binaries, and they must be static, because liken
# ships no shared libraries.
#
# Unlike the other vendored domains, there is nothing trustworthy to
# download: nobody publishes static builds of open-iscsi. So this
# script builds them, from source tarballs pinned by sha256, inside a
# container image pinned by digest, which fixes the toolchain the way
# the pins fix the source. (Talos compiles these same binaries for its
# iscsi-tools extension, a useful independent comparison when
# auditing.) This is the repo's first build-from-source vendor, and
# the first to need a container runtime on the build host: docker or
# podman, whichever is present.
#
# Two more pinned sources ride along, because a fully static link
# needs static libraries that alpine doesn't package:
#
#   kmod      iscsid links libkmod (it checks that iscsi_tcp is
#             loaded); alpine ships only the shared library.
#   libeconf  alpine's static libblkid references it (open-iscsi's
#             is-it-mounted check links libmount, which pulls in
#             libblkid); alpine ships only the shared library.
#
# Usage:
#   open-iscsi/fetch.sh    build the version pinned in open-iscsi/VERSION
#
# Results land in open-iscsi/dist/<version>/, with the source
# tarballs cached in open-iscsi/cache/.

set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

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
[[ -n "$runtime" ]] || {
    echo "fetch.sh: needs docker or podman to run the pinned build container" >&2
    exit 1
}

version="${1:-$(cat "$here/VERSION")}"

# Every input pinned by hash: the builder by image digest, each source
# by the sha256 of its tarball. Bumping any of them is a reviewable
# diff on this file. The open-iscsi pin matches the version in
# open-iscsi/VERSION; building any other version means updating both.
builder="docker.io/library/alpine@sha256:14358309a308569c32bdc37e2e0e9694be33a9d99e68afb0f5ff33cc1f695dce" # 3.22
openiscsi_sha256="f288d1823b15782432608e5f53723159562e2c44e9a72b40fe15a5ca064ac86a"
kmod_version="34"
kmod_sha256="12e7884484151fbd432b6a520170ea185c159f4393c7a2c2a886ab820313149a"
libeconf_version="0.7.9"
libeconf_sha256="0605f8d8a2f4668cb16e279ebcad8002cc83f44610633157e9c4b8fc183a479b"

cache="$here/cache/$version"
out="$here/dist/$version"
mkdir -p "$cache" "$out"

# Download once, verify every time: a cached tarball that stops
# matching its pin fails the build rather than feeding it.
fetch() {
    local url="$1" sha="$2" file="$cache/$3"
    if [[ ! -f "$file" ]]; then
        curl -fsSL "$url" -o "$file.partial"
        mv "$file.partial" "$file"
    fi
    echo "$sha  $file" | sha256sum --check --quiet
}

fetch "https://github.com/open-iscsi/open-iscsi/archive/refs/tags/$version.tar.gz" \
    "$openiscsi_sha256" "open-iscsi-$version.tar.gz"
fetch "https://www.kernel.org/pub/linux/utils/kernel/kmod/kmod-$kmod_version.tar.xz" \
    "$kmod_sha256" "kmod-$kmod_version.tar.xz"
fetch "https://github.com/openSUSE/libeconf/archive/refs/tags/v$libeconf_version.tar.gz" \
    "$libeconf_sha256" "libeconf-$libeconf_version.tar.gz"

# The build itself, inside the pinned container: sources mounted
# read-only at /in, the dist directory writable at /out, and the
# script below piped to the container's shell. Each stage leaves a
# static library the next one links.
"$runtime" run --rm -i \
    -v "$cache:/in:ro" \
    -v "$out:/out" \
    -e VERSION="$version" \
    -e KMOD_VERSION="$kmod_version" \
    -e LIBECONF_VERSION="$libeconf_version" \
    -e HOST_UID="$(id -u)" \
    -e HOST_GID="$(id -g)" \
    "$builder" sh -e <<'BUILD'
apk add --quiet build-base bash meson ninja pkgconf util-linux-dev \
    util-linux-static openssl-dev openssl-libs-static linux-headers file
mkdir /build

# libeconf, static, installed into the toolchain's default prefix.
# Alpine's blkid.pc doesn't declare this dependency for static links,
# so a static consumer of libblkid comes up one library short; the
# appended line says what the packager left out.
tar xzf "/in/libeconf-$LIBECONF_VERSION.tar.gz" -C /build
cd "/build/libeconf-$LIBECONF_VERSION"
meson setup build --prefix=/usr -Ddefault_library=static >/dev/null
ninja -C build install >/dev/null
echo "Libs.private: -leconf" >>/usr/lib/pkgconfig/blkid.pc

# kmod, static, into its own prefix. Upstream hardcodes the public
# libkmod as a shared library, but builds the identical objects into
# an internal static archive for its own tools, so the real libkmod.a
# is assembled from that archive's members (it is a thin archive, so
# copying the file alone would carry references, not objects).
# Compression and tools are disabled: iscsid only ever asks libkmod
# whether iscsi_tcp is loaded (init loads it before k3s starts), never
# to read a module off disk.
tar xJf "/in/kmod-$KMOD_VERSION.tar.xz" -C /build
cd "/build/kmod-$KMOD_VERSION"
meson setup build --prefix=/opt/kmod -Ddefault_library=static \
    -Dzstd=disabled -Dxz=disabled -Dzlib=disabled -Dopenssl=disabled \
    -Dtools=false -Dmanpages=false -Dlogging=false >/dev/null
ninja -C build install >/dev/null
cd build
ar crs /opt/kmod/lib/libkmod.a $(ar t libkmod-internal.a) $(ar t libshared.a)
rm /opt/kmod/lib/libkmod.so*

# open-iscsi itself. Two small patches to its build definition, both
# consequences of a fully static link upstream never aimed for: the
# internal libopeniscsiusr is hardcoded shared (a shared library can't
# be built with -static in the link line), and version: is a kwarg
# only shared libraries accept. The pkg-config block's own version
# line is indented differently and survives the second sed. iSNS is a
# discovery directory service this feature doesn't offer; disabling it
# drops the open-isns dependency entirely.
tar xzf "/in/open-iscsi-$VERSION.tar.gz" -C /build
cd "/build/open-iscsi-$VERSION"
sed -i 's/libiscsi_usr = shared_library(/libiscsi_usr = static_library(/' meson.build
sed -i "/^  version: '0.2.0',/d" meson.build
PKG_CONFIG_PATH=/opt/kmod/lib/pkgconfig \
    meson setup build -Dno_systemd=true -Disns=disabled \
    -Ddefault_library=static -Dprefer_static=true -Dc_link_args=-static >/dev/null
ninja -C build iscsid iscsiadm >/dev/null

# A dynamically linked binary here would run fine in this container
# and fail on the machine, which has no loader and no libraries;
# refuse to produce one.
strip build/iscsid build/iscsiadm
for bin in iscsid iscsiadm; do
    file "build/$bin" | grep -q "statically linked" || {
        echo "fetch.sh: $bin is not statically linked" >&2
        exit 1
    }
    install -m 0755 "build/$bin" "/out/$bin"
done
chown "$HOST_UID:$HOST_GID" /out/iscsid /out/iscsiadm
BUILD

echo "open-iscsi $version:"
(cd "$out" && sha256sum iscsid iscsiadm)
