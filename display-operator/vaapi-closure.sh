#!/bin/sh
# Collects the VA-API loader and the Intel media driver into one directory
# tree, less every file the vulkan tree holds. The tree is the
# ghcr.io/liken-sh/vaapi image, the base for a program that decodes
# video on the node's GPU.
#
# Mesa's VA driver for AMD is left out. On Debian it is a link into
# libgallium, which the vulkan tree does not hold, so the seed would
# add 40 MB for a card no liken machine decodes on yet. A machine with
# AMD graphics decodes in software until that changes.
#
# Run it in a builder that has the packages installed. It writes a
# rootfs to the first directory named on the command line, less every
# file the second directory already holds.
set -eu

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
"

# Neither libva nor the Intel driver reads a data file Debian ships.
data=""

collect "$out" "$seeds" "$data"
subtract "$out" "$base"
