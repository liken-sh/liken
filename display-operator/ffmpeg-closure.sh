#!/bin/sh
# Collects ffmpeg and ffprobe and everything they load into one directory
# tree, less every file the trees named after it hold. The tree is the
# ghcr.io/liken-sh/ffmpeg image.
#
# Run it in a builder that has the packages installed. It writes a
# rootfs to the first directory named on the command line, less every
# file the directories after it already hold.
set -eu

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

# The image is FROM vaapi, which is FROM vulkan, so the layer carries
# neither tree.
for base in "$@"; do
	subtract "$out" "$base"
done
