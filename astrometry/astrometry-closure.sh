#!/bin/sh
# Collects astrometry-engine, solve-field, and source-extractor and
# everything they load into one directory tree. The tree is the
# ghcr.io/liken-sh/astrometry image.
#
# Run it in a builder that has the packages installed. It writes a
# rootfs to the directory named on the command line.
set -eu

# closure.sh is in the vulkan directory, and the Dockerfile copies it
# beside this script.
# shellcheck source=../vulkan/closure.sh
. "$(dirname "$0")/closure.sh"

out=$1

# Debian's astrometry.net package also holds Python tools, and the
# package depends on Python. The closure follows only what the loader
# resolves for these three programs, so no Python reaches the tree.
# solve-field runs Python only for --just-augment without
# --no-remove-lines and --uniformize 0, and the worker passes both.
seeds="
/usr/bin/astrometry-engine
/usr/bin/solve-field
/usr/bin/source-extractor
"

# The convolution filters and the neural network of SourceExtractor,
# which it opens by the path that FILTER_NAME and STARNNW_NAME give.
# They are a few kilobytes, and a caller that filters or classifies
# stars names one of them.
data="
/usr/share/source-extractor
"

collect "$out" "$seeds" "$data"
notices "$out" astrometry

# solve-field writes its intermediate files to /tmp, and scratch has
# no /tmp.
mkdir -m 1777 "$out/tmp"
