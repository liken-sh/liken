#!/bin/sh
# Collects ffmpeg and ffprobe and everything they load into one directory
# tree, less every file of the base image under it. The tree is the
# ghcr.io/liken-sh/ffmpeg image.
#
# Run it in a builder that has the packages installed. It writes a
# rootfs to the first directory named on the command line, less every
# file the second directory already holds. The second is the tree of
# the vaapi image, which holds the vulkan tree too.
set -eu

# closure.sh is in the vulkan directory, the lowest base, and the
# Dockerfile copies it beside this script.
# shellcheck source=../vulkan/closure.sh
. "$(dirname "$0")/closure.sh"

out=$1
shift

seeds="
/usr/bin/ffmpeg
/usr/bin/ffprobe
"

# /usr/share/ffmpeg holds the libvpx presets and an XSD, and neither
# program opens one unless an option names it.
data=""

collect "$out" "$seeds" "$data"
notices "$out" ffmpeg

# The image is FROM vaapi, which is FROM vulkan, so the layer carries
# neither tree.
subtract "$out" "$1"
