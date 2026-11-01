#!/usr/bin/env bash
#
# Write the CycloneDX SBOM of one release build:
#
#   releases/sbom/scan.sh <version> <out> [<root filesystem> <toolkit>]
#
# releases/Makefile runs this after the build and before the bundle,
# so the bundle names the SBOM in release.yaml by its digest like
# every other file of the release. The root filesystem is the tree
# that image/build.sh stages before mksquashfs, so syft reads the same
# files that liken.sqfs holds, and the toolkit is the liken binary
# that the release ships.
#
# The lab's working-tree channel names no root filesystem, so its SBOM
# lists only what the manifests and buildroot name. A `make` of the
# lab then runs no scan, and the channel keeps the layout of a
# release.
#
# syft reads what a program records about itself: the build info of a
# Go binary, and the package databases of an image. It scans each part
# of the release that no manifest and no buildroot SBOM describes in
# full:
#
#   root.cdx.json         the system image's root filesystem
#   <image>.cdx.json      each container image archive that the image
#                         carries for k3s to import, such as the
#                         operators and iscsid
#   toolkit.cdx.json      the liken toolkit
#   k3s.cdx.json          the programs that k3s carries inside its own
#                         binary: containerd, runc, the CNI plugins,
#                         busybox, and the Kubernetes binary itself
#
# k3s keeps those programs in an archive inside its binary, and writes
# them to its data directory before it runs one of them, so syft sees
# none of them in /bin/k3s. The script runs the pinned k3s once with
# `ctr --version`, a command that only prints, with its data directory
# beside the scans, so k3s writes out the programs that every machine
# of the release runs. The k3s binary is static and the build runs on
# amd64, so the host runs it with no container.
#
# releases/sbom then merges the scans with each pin's manifest and
# buildroot's SBOM, and checks the result against the CycloneDX
# schema. Its main.go gives the merge.
#
# syft runs from the image that toolchains/images.toml pins, through
# docker or podman, as the calling user. The image has no writable
# /tmp, which syft unpacks an image archive into, so the container
# gets a tmpfs there.

set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source-path=SCRIPTDIR source=../../../toolchains/pin.sh
source "$here/../../../toolchains/pin.sh"

usage="usage: scan.sh <version> <out> [<root filesystem> <toolkit>]"
version="${1:?$usage}"
out="${2:?$usage}"
mkdir -p "$(dirname "$out")"
out="$(cd "$(dirname "$out")" && pwd)/$(basename "$out")"
root="${3:-}"
toolkit="${4:-}"
liken="$(cd "$here/../.." && pwd)"

args=(-version "$version" -out "$out" -buildroot "$liken/userland/dist/sbom.cdx.json")

# scan <source> <path> <name>: run syft over a path, mounted read-only,
# and write <name>.cdx.json. The source is syft's scheme for the path:
# dir, oci-archive, or file. syft gives the location of a package in a
# file source as the file's name in the container, so a file is mounted
# under its own name, and the toolkit's location reads `liken`, its
# name in the release. A directory or an archive is mounted at /scan,
# because syft gives each location inside it from its root.
scan() {
    local source="$1" path="$2" name="$3"
    local mount=/scan
    [[ "$source" == file ]] && mount="/$(basename "$path")"
    "$runtime" run --rm --user "$(id -u):$(id -g)" -e HOME=/tmp --tmpfs /tmp:exec,mode=1777 \
        -v "$path:$mount:ro" -v "$scan_dir:/out" "$syft" \
        scan "$source:$mount" -q -o "cyclonedx-json=/out/$name.cdx.json"
}

if [[ -n "$root" ]]; then
    [[ -d "$root" && -f "$toolkit" ]] || fail "$usage"
    root="$(cd "$root" && pwd)"
    toolkit="$(cd "$(dirname "$toolkit")" && pwd)/$(basename "$toolkit")"
    runtime="$(container_runtime)"
    [[ -n "$runtime" ]] || fail "needs docker or podman to run syft"
    syft="$(image_of syft)"
    scan_dir="${out%.cdx.json}-scans"
    rm -rf "$scan_dir"
    mkdir -p "$scan_dir"

    scan dir "$root" root
    args+=(-syft "liken=$scan_dir/root.cdx.json")
    for archive in "$root"/var/lib/rancher/k3s/agent/images/*.tar; do
        name="$(basename "$archive" .tar)"
        scan oci-archive "$archive" "$name"
        args+=(-syft "liken=$scan_dir/$name.cdx.json")
    done
    scan file "$toolkit" toolkit
    args+=(-syft "liken=$scan_dir/toolkit.cdx.json")

    "$root/bin/k3s" --data-dir "$scan_dir/k3s-data" ctr --version >/dev/null 2>&1 ||
        fail "k3s did not write out its programs into $scan_dir/k3s-data"
    scan dir "$(readlink -f "$scan_dir/k3s-data/data/current")" k3s
    args+=(-syft "pin:k3s=$scan_dir/k3s.cdx.json")
fi

# A claim names the manifest or the buildroot package that already
# lists the program at a path, so the SBOM keeps that component, with
# its purl and its license, in place of syft's. image/build.sh puts
# each of these files at its path. k3s's own binary is at /bin/k3s in
# both the root filesystem and its data directory. A claim that ends
# in a slash takes each file under the directory: syft lists each
# kernel module as a package, with no version, and the linux-modules
# entry of kernel/pin.toml names the package that holds them all.
args+=(
    -claim /bin/k3s=pin:k3s
    -claim /lib/modules/=pin:linux-modules
    -claim /lib/firmware/=pin:linux-firmware
    -claim /sbin/xtables-legacy-multi=pin:k3s-root-xtables
    -claim /sbin/mke2fs=buildroot:e2fsprogs
    -claim /sbin/mount.nfs=buildroot:nfs-utils
    -claim /sbin/wpa_supplicant=buildroot:wpa_supplicant
)

# Each pin whose files a release ships holds a manifest, and `ci pins`
# fails one that does not. The OS's pins are each a domain of liken/,
# and trust, the CA bundle, is a component of its own.
(cd "$liken" && go run ./releases/sbom "${args[@]}" "$liken"/*/pin.toml "$liken/../trust/pin.toml")
echo "wrote $out: $(jq '.components | length' "$out") components"
