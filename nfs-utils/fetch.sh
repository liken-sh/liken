#!/usr/bin/env bash
#
# Vendor the NFS client: a static mount.nfs, the whole host half of
# the nfs feature (see plans/17-network-storage-clients.md).
#
# The kernel does everything else. An NFSv4 mount is one TCP
# connection to port 2049, with locking carried by the protocol's own
# leases, so there are no daemons to run: the feature is this one
# binary, which the kernel's mount syscall path execs as the "nfs"
# filesystem's mount helper, plus the nfsv4 module. (NFSv3 would drag
# rpcbind and rpc.statd onto the host, two daemons k3s does not depend
# on, which the two-planes rule refuses; this feature is v4 only.)
#
# Like open-iscsi, there is nothing trustworthy to download, so this
# script builds from pinned sources inside the same digest-pinned
# container: nfs-utils itself, and libtirpc, the RPC library mount.nfs
# speaks the mount protocol through, built in statically.
#
# Usage:
#   nfs-utils/fetch.sh    build the version pinned in nfs-utils/VERSION
#
# Results land in nfs-utils/dist/<version>/, with the source tarballs
# cached in nfs-utils/cache/.

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

# Every input pinned by hash, the same discipline as every vendored
# domain. The nfs-utils pin matches nfs-utils/VERSION; building any
# other version means updating both.
builder="docker.io/library/alpine@sha256:14358309a308569c32bdc37e2e0e9694be33a9d99e68afb0f5ff33cc1f695dce" # 3.22
nfsutils_sha256="11e7c5847a8423a72931c865bd9296e7fd56ff270a795a849183900961711725"
libtirpc_version="1.3.6"
libtirpc_sha256="bbd26a8f0df5690a62a47f6aa30f797f3ef8d02560d1bc449a83066b5a1d3508"

cache="$here/cache/$version"
out="$here/dist/$version"
mkdir -p "$cache" "$out"

# Download once, verify every time.
fetch() {
    local url="$1" sha="$2" file="$cache/$3"
    if [[ ! -f "$file" ]]; then
        curl -fsSL "$url" -o "$file.partial"
        mv "$file.partial" "$file"
    fi
    echo "$sha  $file" | sha256sum --check --quiet
}

fetch "https://www.kernel.org/pub/linux/utils/nfs-utils/$version/nfs-utils-$version.tar.xz" \
    "$nfsutils_sha256" "nfs-utils-$version.tar.xz"
fetch "https://downloads.sourceforge.net/project/libtirpc/libtirpc/$libtirpc_version/libtirpc-$libtirpc_version.tar.bz2" \
    "$libtirpc_sha256" "libtirpc-$libtirpc_version.tar.bz2"

# The build, inside the pinned container. nfs-utils is one source tree
# carrying a dozen programs, and this build wants exactly one of them,
# so configure's job here is mostly saying no: no GSS/Kerberos, no
# NFSv4 server-side tooling (idmapd and friends serve nfsd, not the
# client), no udev readahead helper, no systemd units. The libevent
# and sqlite packages exist only to satisfy configure's unconditional
# checks; nothing mount.nfs links needs them.
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
    libevent-dev libevent-static sqlite-dev sqlite-static
mkdir /build

# libtirpc, static, into the toolchain's default prefix so both
# configure's probe (a bare -ltirpc) and the final link find it. The
# bsd-compat-headers package supplies sys/queue.h, a BSD-ism musl
# leaves out. GSS is Kerberos for RPC, and nothing here speaks it.
tar xjf "/in/libtirpc-$LIBTIRPC_VERSION.tar.bz2" -C /build
cd "/build/libtirpc-$LIBTIRPC_VERSION"
./configure --prefix=/usr --disable-shared --enable-static --disable-gssapi >/dev/null
make -j"$(nproc)" install >/dev/null

# mount.nfs. The link asks libtool for -all-static, because libtool
# quietly repurposes a plain -static for its own bookkeeping and
# produces a dynamic binary anyway.
tar xJf "/in/nfs-utils-$VERSION.tar.xz" -C /build
cd "/build/nfs-utils-$VERSION"
./configure --disable-gss --disable-nfsv4 --disable-nfsv41 \
    --disable-uuid --disable-caps --without-systemd \
    --disable-nfsdcld --disable-nfsdctl --disable-junction \
    --disable-nfsrahead >/dev/null
make -j"$(nproc)" -C support >/dev/null
make -j"$(nproc)" -C utils/mount LDFLAGS="-all-static" >/dev/null

# A dynamic binary would run in this container and fail on the
# machine, which has no loader; refuse to produce one.
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
