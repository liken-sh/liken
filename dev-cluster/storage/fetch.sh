#!/usr/bin/env bash
#
# Fetch the lab storage server's operating system: a stock Debian
# cloud image.
#
# The storage server is the one guest in the lab that is not a liken
# machine, on purpose. The iscsi and nfs features make liken a client
# of network storage. To drill a client honestly, the drill must
# point it at a server that liken had no hand in: an ordinary Linux
# box that runs the reference implementations (the kernel NFS server
# and a standard iSCSI target). A mainstream distribution is the
# fastest honest way to get one, and Debian's cloud images are built
# for exactly this use. Each image is a generic qcow2 file that boots
# in any hypervisor and configures itself from a cloud-init seed on
# first boot, with no installer and no interaction (see seed/ and the
# Makefile).
#
# The pin is a dated build (storage/VERSION). Debian publishes each
# cloud image build into an immutable directory, named by date and
# serial, with checksums stored beside it. Because of this, the fetch
# verifies the image against the SHA512SUMS file published in the
# same directory. This is the same approach that trust/fetch.sh
# takes, where the pin is the snapshot and the checksum travels with
# it.
#
# Usage:
#   fetch.sh                fetch the build pinned in storage/VERSION
#   fetch.sh 20260706-2531  fetch a specific build instead
#
# The image lands in storage/cache/<version>/, where the Makefile
# uses it read-only as the backing file for the guest's root disk.

set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

for tool in curl sha512sum; do
    command -v "$tool" >/dev/null || {
        echo "fetch.sh: missing required tool: $tool" >&2
        exit 1
    }
done

version="${1:-$(cat "$here/VERSION")}"
image="debian-13-genericcloud-amd64-$version.qcow2"
url="https://cloud.debian.org/images/cloud/trixie/$version"

cache="$here/cache/$version"
mkdir -p "$cache"

digest="$(curl -fsSL "$url/SHA512SUMS" | awk -v img="$image" '$2 == img { print $1 }')"
if [[ -z "$digest" ]]; then
    echo "fetch.sh: no checksum for $image published at $url/SHA512SUMS" >&2
    exit 1
fi

if ! sha512sum --check --status <<<"$digest  $cache/$image" >/dev/null 2>&1; then
    echo "downloading Debian cloud image $version"
    curl -fL --progress-bar -o "$cache/$image" "$url/$image"
    sha512sum --check --quiet <<<"$digest  $cache/$image"
fi

echo "Debian cloud image $version:"
qemu-img info "$cache/$image" | sed -n '1,4p'
