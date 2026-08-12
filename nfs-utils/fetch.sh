#!/usr/bin/env bash
#
# Vendor the NFS client: a static mount.nfs, the whole host half of
# the nfs feature (see plans/completed/17-network-storage-clients.md).
#
# The kernel does everything else. An NFSv4 mount is one TCP
# connection to port 2049, with locking carried by the protocol's own
# leases, so there are no daemons to run. The feature is this one
# binary, which the kernel's mount syscall path runs as the "nfs"
# filesystem's mount helper, plus the nfsv4 module. (NFSv3 would put
# rpcbind and rpc.statd on the host, two daemons k3s does not depend
# on, and the two-planes rule refuses that. So this feature is v4
# only.)
#
# As with open-iscsi, there is nothing trustworthy to download, so
# this script builds from pinned sources inside the same
# digest-pinned container: nfs-utils itself, and libtirpc, the RPC
# library mount.nfs uses for the mount protocol, built in statically.
#
# Usage:
#   nfs-utils/fetch.sh                   build the version pinned in
#                                        nfs-utils/VERSION
#   nfs-utils/fetch.sh --sources-only    fetch and verify the source
#                                        tarballs, skipping the build
#
# --sources-only exists for the licensing domain: the release channel
# mirrors these same tarballs as the source that corresponds to the
# binary. Mirroring needs the verified bytes, not the build, and it
# has no container runtime.
#
# Results land in nfs-utils/dist/<version>/, with the source tarballs
# cached in nfs-utils/cache/.

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
    echo "fetch.sh: needs docker or podman to run the pinned build container" >&2
    exit 1
}

version="$(cat "$here/VERSION")"

# Every input is pinned by hash, the same discipline as every vendored
# domain. The nfs-utils pin matches nfs-utils/VERSION. To build any
# other version, update both.
builder="docker.io/library/alpine@sha256:14358309a308569c32bdc37e2e0e9694be33a9d99e68afb0f5ff33cc1f695dce" # 3.22
nfsutils_sha256="e1dd8a9c95af15492065942cc3b52b1339ffd586baa2280ed86c9d3dc4097e8c"
libtirpc_version="1.3.7"
libtirpc_sha256="b47d3ac19d3549e54a05d0019a6c400674da716123858cfdb6d3bdd70a66c702"

cache="$here/cache/$version"
out="$here/dist/$version"
mkdir -p "$cache" "$out"

# Download once, verify every time. If a cached tarball stops matching
# its pin, the build fails instead of using the tarball.
fetch() {
    local url="$1" sha="$2" file="$cache/$3"
    if [[ ! -f "$file" ]]; then
        curl -fsSL --retry 5 --retry-all-errors --retry-delay 5 -C - \
            "$url" -o "$file.partial"
        mv "$file.partial" "$file"
    fi
    echo "$sha  $file" | sha256sum --check --quiet
}

fetch "https://www.kernel.org/pub/linux/utils/nfs-utils/$version/nfs-utils-$version.tar.xz" \
    "$nfsutils_sha256" "nfs-utils-$version.tar.xz"
fetch "https://downloads.sourceforge.net/project/libtirpc/libtirpc/$libtirpc_version/libtirpc-$libtirpc_version.tar.bz2" \
    "$libtirpc_sha256" "libtirpc-$libtirpc_version.tar.bz2"

[[ -z "$sources_only" ]] || exit 0

# The build runs inside the pinned container. nfs-utils is one source
# tree carrying a dozen programs, and this build needs exactly one of
# them, so configure here mostly disables things: no GSS/Kerberos, no
# NFSv4 server-side tooling (idmapd and related tools serve nfsd, not
# the client), no udev readahead helper, no systemd units. The
# libevent, sqlite, and libnl3 packages exist only to satisfy
# configure's unconditional checks; nothing mount.nfs links needs
# them. The netlink code that wants libnl3 serves nfsd's control
# plane, which --disable-nfsdctl already turns off.
"$runtime" run --rm -i \
    -v "$cache:/in:ro" \
    -v "$out:/out" \
    -e VERSION="$version" \
    -e LIBTIRPC_VERSION="$libtirpc_version" \
    -e HOST_UID="$(id -u)" \
    -e HOST_GID="$(id -g)" \
    "$builder" sh -e <<'BUILD'
apk add --quiet build-base bash pkgconf linux-headers file rpcgen \
    util-linux-dev util-linux-static bzip2 xz bsd-compat-headers \
    libevent-dev libevent-static sqlite-dev sqlite-static libnl3-dev
mkdir /build

# This installs libtirpc, static, into the toolchain's default prefix,
# so both configure's probe (a bare -ltirpc) and the final link find
# it. The bsd-compat-headers package supplies sys/queue.h, a BSD
# header that musl leaves out. GSS is Kerberos for RPC, and nothing
# here uses it.
#
# --enable-rpcdb keeps getrpcbynumber and its relatives in the
# library. Their build is off by default because glibc carries its
# own copies, but musl does not, and the nfs-utils configure below
# refuses to run without getrpcbynumber. On musl, libtirpc is the
# only provider.
tar xjf "/in/libtirpc-$LIBTIRPC_VERSION.tar.bz2" -C /build
cd "/build/libtirpc-$LIBTIRPC_VERSION"
./configure --prefix=/usr --disable-shared --enable-static --disable-gssapi \
    --enable-rpcdb >/dev/null
make -j"$(nproc)" install >/dev/null

# This builds mount.nfs. The link passes -all-static to libtool,
# because libtool silently reuses a plain -static flag for its own
# bookkeeping and produces a dynamic binary anyway.
#
# PKG_CONFIG is preset because configure's own pkg-config detection
# sits inside the readahead block that --disable-nfsrahead skips, so
# every later PKG_CHECK_MODULES call would report pkg-config missing.
#
# CFLAGS adds -include stddef.h because support/nfs/getport.c uses
# offsetof without including that header. glibc's headers pull it in
# transitively and musl's do not, so the preprocessor reads it first
# here. This works around an upstream bug; a release that adds the
# include makes the flag unnecessary.
tar xJf "/in/nfs-utils-$VERSION.tar.xz" -C /build
cd "/build/nfs-utils-$VERSION"
./configure --disable-gss --disable-nfsv4 \
    --disable-uuid --disable-caps --without-systemd \
    --disable-nfsdcld --disable-nfsdctl --disable-junction \
    --disable-nfsrahead \
    PKG_CONFIG=/usr/bin/pkg-config \
    CFLAGS="-g -O2 -include stddef.h" >/dev/null
make -j"$(nproc)" -C support >/dev/null
make -j"$(nproc)" -C utils/mount LDFLAGS="-all-static" >/dev/null

# A dynamic binary would run in this container and fail on the
# machine, which has no loader. This step refuses to produce one.
strip utils/mount/mount.nfs
file utils/mount/mount.nfs | grep -q "statically linked" || {
    echo "fetch.sh: mount.nfs is not statically linked" >&2
    exit 1
}
install -m 0755 utils/mount/mount.nfs /out/mount.nfs
chown "$HOST_UID:$HOST_GID" /out/mount.nfs
BUILD

echo "nfs-utils $version:"
(cd "$out" && sha256sum mount.nfs)
