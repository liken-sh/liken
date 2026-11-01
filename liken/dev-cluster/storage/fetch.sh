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
# suite below), and the image's sha512 digest. Debian publishes each
# cloud image build into its own directory, named by date and serial,
# with a SHA512SUMS file beside it. storage/latest.sh --bump copies
# the image's line from that file into this one, so a fetch never
# reads a digest from upstream, and an image that changes upstream
# after the bump fails the check.
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
# A build other than the pinned one needs its digest in the table
# below first.
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

# The sha512 digest of each pinned build's image, from the build's
# SHA512SUMS file. storage/latest.sh --bump writes it.
declare -A image_sha512=(
    ["20261017-2634"]="ec584ccabfef7120b4c4f7482adf0e615894d9292cedec446b64194b951ebe5dd97c336b5dcbae458333764b0edd516334506017f33c4f2020ef8d38b569ffe3"
)
digest="${image_sha512[$version]:-}"
if [[ -z "$digest" ]]; then
    echo "fetch.sh: no digest pinned for the storage image $version; storage/latest.sh --bump writes it" >&2
    exit 1
fi

if ! sha512sum --check --status <<<"$digest  $cache/$image" >/dev/null 2>&1; then
    echo "downloading Debian cloud image $version"
    # The cloud image is large, and a long stream sometimes breaks
    # partway through. -C - resumes each retry from the bytes already
    # on disk, so repeated breaks still converge on a complete file.
    # The checksum below decides whether the assembled bytes are good.
    # The bytes go to a .partial file, which replaces the cached file
    # only after it matches the digest. A .partial that does not match
    # is deleted: when the file on disk is already full length, curl -C
    # - gets a 416 answer, exits with success, and leaves the bytes as
    # they are, so a bad full-length file would fail every later run.
    curl -fL --retry 5 --retry-all-errors --retry-delay 5 --max-time 1800 -C - \
        --progress-bar -o "$cache/$image.partial" "$url/$image"
    sha512sum --check --quiet <<<"$digest  $cache/$image.partial" || {
        rm -f "$cache/$image.partial"
        exit 1
    }
    mv "$cache/$image.partial" "$cache/$image"
fi
ln -sfn "$image" "$cache/base.qcow2"

echo "Debian cloud image $version:"
qemu-img info "$cache/$image" | sed -n '1,4p'
