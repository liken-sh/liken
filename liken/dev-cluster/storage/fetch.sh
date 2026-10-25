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
# The pin is a dated build (storage/VERSION) of one Debian suite (the
# suite below). Debian publishes each cloud image build into its own
# directory, named by date and serial, with checksums stored beside
# it. Because of this, the fetch verifies the image against the
# SHA512SUMS file published in the same directory. This is the same
# approach that trust/fetch.sh takes, where the pin is the snapshot
# and the checksum travels with it.
#
# The two suites publish under different paths and names. A release
# such as trixie keeps each build in images/cloud/trixie/<build>/, as
# debian-13-genericcloud-amd64-<build>.qcow2, for the life of the
# release. sid has no release builds, only daily ones, in
# images/cloud/sid/daily/<build>/, as
# debian-sid-genericcloud-amd64-daily-<build>.qcow2. Debian deletes a
# daily build after about four months, so a sid pin must move before
# then, or this fetch fails.
#
# Usage:
#   fetch.sh                      fetch the build pinned in
#                                 storage/VERSION, from the suite below
#   fetch.sh 20260706-2531        fetch another build of that suite
#   fetch.sh 20261023-2640 sid    fetch a build of another suite
#
# The image lands in storage/cache/<version>/, and base.qcow2 beside
# it names the image. The Makefile uses base.qcow2 read-only as the
# backing file for the guest's root disk, so it needs no knowledge of
# the suite's naming.

set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

for tool in curl sha512sum; do
    command -v "$tool" >/dev/null || {
        echo "fetch.sh: missing required tool: $tool" >&2
        exit 1
    }
done

# The suite that the pin in VERSION comes from. latest.sh reads this
# line, and its --bump writes it.
suite="trixie"

version="${1:-$(cat "$here/VERSION")}"
suite="${2:-$suite}"
images="https://cloud.debian.org/images/cloud"
case "$suite" in
trixie)
    image="debian-13-genericcloud-amd64-$version.qcow2"
    url="$images/trixie/$version"
    ;;
sid)
    image="debian-sid-genericcloud-amd64-daily-$version.qcow2"
    url="$images/sid/daily/$version"
    ;;
*)
    echo "fetch.sh: no image naming known for the suite $suite" >&2
    exit 1
    ;;
esac

cache="$here/cache/$version"
mkdir -p "$cache"

digest="$(curl -fsSL --retry 3 --max-time 60 "$url/SHA512SUMS" | awk -v img="$image" '$2 == img { print $1 }')"
if [[ -z "$digest" ]]; then
    echo "fetch.sh: no checksum for $image published at $url/SHA512SUMS" >&2
    exit 1
fi

if ! sha512sum --check --status <<<"$digest  $cache/$image" >/dev/null 2>&1; then
    echo "downloading Debian cloud image $version"
    # The cloud image is large, and a long stream sometimes breaks
    # partway through. -C - resumes each retry from the bytes already
    # on disk, so repeated breaks still converge on a complete file.
    # The checksum below decides whether the assembled bytes are good.
    curl -fL --retry 5 --retry-all-errors --retry-delay 5 --max-time 1800 -C - \
        --progress-bar -o "$cache/$image" "$url/$image"
    sha512sum --check --quiet <<<"$digest  $cache/$image"
fi
ln -sfn "$image" "$cache/base.qcow2"

echo "Debian cloud image $version:"
qemu-img info "$cache/$image" | sed -n '1,4p'
