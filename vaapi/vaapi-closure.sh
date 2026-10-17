#!/bin/sh
# Collects the VA-API loader, the Intel media driver, and Mesa's VA
# driver for AMD into one directory tree, less every file the vulkan
# tree holds. The tree is the ghcr.io/liken-sh/vaapi image, the base for
# a program that decodes video on the node's GPU.
#
# Run it in a builder that has the packages installed. It writes a
# rootfs to the first directory named on the command line, less every
# file the second directory already holds.
set -eu

# closure.sh is in the vulkan directory, the lowest base, and the
# Dockerfile copies it beside this script.
# shellcheck source=../vulkan/closure.sh
. "$(dirname "$0")/closure.sh"

out=$1
base=$2

# libva opens the driver from its compiled-in dri directory, under the
# name the kernel driver reports, so the driver is in no program's
# DT_NEEDED graph and is a seed here.
seeds="
$lib/libva.so.2
$lib/libva-drm.so.2
$lib/dri/iHD_drv_video.so
$lib/dri/radeonsi_drv_video.so
"

# Mesa's VA driver reads the drirc defaults, which the vulkan tree
# holds. libva and the Intel driver read no data file Debian ships.
data=""

collect "$out" "$seeds" "$data"
notices "$out" vaapi
subtract "$out" "$base"
