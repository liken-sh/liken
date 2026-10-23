#!/usr/bin/env bash
#
# Build the lab's capsule firmware: OVMF from a pinned edk2 release,
# with a firmware device that fwupd can update through a UEFI capsule.
#
# Debian's OVMF, which the other FIRMWARE modes boot, links
# DxeCapsuleLibNull, publishes no ESRT, and has no Firmware Management
# Protocol (FMP) device, so fwupd on a guest finds nothing to update.
# This build adds DxeCapsuleLibFmp, EsrtFmpDxe, and one FmpDxe instance.
# Its device has no hardware: the firmware version is a number in the
# UEFI variable LabFmpVersion (OvmfPkg/Library/LabFmpDeviceLib), which
# starts at 1 and takes the version of each capsule that FmpDxe
# accepts. ovmf-capsule.patch holds every change to edk2, and the test
# device's GUID is b0792605-c842-408f-adf2-edfb07638e83.
#
# FmpDxe accepts a capsule only when edk2's test root certificate
# signs it. That key is public in the edk2 tree, which is acceptable
# for a lab device and never for real firmware. catalog.sh signs the
# lab's capsules with it.
#
# The firmware takes a capsule through UpdateCapsule() while boot
# services run, which is how fwupd's EFI updater, fwupdx64.efi,
# delivers it from a BootNext entry. It cannot take a capsule that sets
# PersistAcrossReset, because OVMF has no capsule coalescing in PEI to
# keep the capsule across a reset, and CapsuleRuntimeDxe refuses it
# with EFI_UNSUPPORTED. fwupdx64.efi adds PersistAcrossReset and
# InitiateReset to a capsule whose header Flags field is zero, so the
# lab's capsules set an OEM flag that edk2 ignores (catalog.sh). Most
# vendor capsules set PersistAcrossReset themselves, and this firmware
# refuses them.
#
# The firmware advertises no capsule-on-disk delivery, so fwupd always
# installs through fwupdx64.efi and BootNext, as it does on liken-1.
#
# The build is DEBUG, because FmpDxe and DxeCapsuleLibFmp log why they
# refuse a capsule only in a DEBUG build. OVMF writes that log to the
# debug console at I/O port 0x402, and FIRMWARE=capsule in
# dev-cluster/Makefile saves it to guests/<node>/ovmf-debug.log.
#
# Every step runs in the image that ./Dockerfile describes, as the
# calling user, so the host needs only docker and git, and the files
# stay owned by that user.
#
# Usage:
#   dev-cluster/firmware/capsule/build.sh
#   CAPSULE_ON_DISK=1 dev-cluster/firmware/capsule/build.sh
#
# Results land in firmware/capsule/dist/: OVMF_CODE_4M.fd and
# OVMF_VARS_4M.fd. With CAPSULE_ON_DISK=1, the firmware also reads a
# capsule that fwupd leaves in \EFI\UpdateCapsule on the ESP, and
# advertises it in OsIndicationsSupported, so fwupd stages a capsule on
# disk and not in NVRAM. That build lands in
# firmware/capsule/dist/on-disk/. The edk2 checkout stays in firmware/capsule/.cache/.
# The directory's name starts with a dot because the checkout holds Go
# files of its own, and the go tool's ./... pattern skips a directory
# whose name starts with a dot. Without it, `go vet ./...` and
# staticcheck in liken/ read edk2's submodules.

set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# edk2-stable202608 is the upstream release that Debian's ovmf-generic
# 2026.08+ds-2 packages, which the other FIRMWARE modes boot. The same
# release keeps the variable store layout the same, so a guest keeps
# its own store when it moves between the two firmware builds.
edk2_tag="edk2-stable202608"
edk2_commit="2970e5699ba6267f3384ffab20f96647578aebc8"

edk2="$here/.cache/edk2"
patch="$here/ovmf-capsule.patch"
out="$here/dist"
on_disk=FALSE
if [ -n "${CAPSULE_ON_DISK:-}" ]; then
    on_disk=TRUE
    out="$here/dist/on-disk"
fi

builder="$("$here/builder.sh")"

if [ ! -d "$edk2/.git" ]; then
    mkdir -p "$here/.cache"
    git clone --depth 1 --branch "$edk2_tag" --recurse-submodules \
        --shallow-submodules https://github.com/tianocore/edk2.git "$edk2"
fi
head="$(git -C "$edk2" rev-parse HEAD)"
if [ "$head" != "$edk2_commit" ]; then
    echo "build.sh: $edk2 is at $head, not $edk2_tag ($edk2_commit)" >&2
    exit 1
fi

# The patch applies once. A tree that already holds it builds as it
# is, and a tree with any other change stops the build, so the firmware
# always matches the tag plus the patch.
if git -C "$edk2" apply --check --reverse "$patch" 2>/dev/null; then
    echo "ovmf-capsule.patch is already applied"
elif git -C "$edk2" apply --check "$patch"; then
    git -C "$edk2" apply "$patch"
else
    echo "build.sh: ovmf-capsule.patch does not apply to $edk2" >&2
    exit 1
fi

# HOME points into the tree because the container has no home for the
# calling user, and BaseTools writes its build configuration there.
docker run --rm --user "$(id -u):$(id -g)" \
    -e HOME=/edk2/Build/.home -e ON_DISK="$on_disk" -v "$edk2:/edk2" -w /edk2 "$builder" \
    bash -euc '
        mkdir -p "$HOME"
        make -s -C BaseTools -j"$(nproc)" >/dev/null
        export WORKSPACE=/edk2 EDK_TOOLS_PATH=/edk2/BaseTools
        set +u
        . ./edksetup.sh BaseTools >/dev/null
        set -u
        build -q -n "$(nproc)" -a X64 -t GCC -b DEBUG \
            -p OvmfPkg/OvmfPkgX64.dsc -D FD_SIZE_4MB \
            -D CAPSULE_ON_DISK_ENABLE="$ON_DISK"
    '

mkdir -p "$out"
fv="$edk2/Build/OvmfX64/DEBUG_GCC/FV"
cp "$fv/OVMF_CODE.fd" "$out/OVMF_CODE_4M.fd"
cp "$fv/OVMF_VARS.fd" "$out/OVMF_VARS_4M.fd"
sha256sum "$out"/*.fd
