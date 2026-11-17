#!/bin/sh
# Collects transect and every library that the loader resolves for it
# into one directory tree, so the image ships that tree and nothing
# else. Run it in a builder that holds the binary at /transect, with the
# output directory as the argument.
#
# The functions come from vulkan/closure.sh, the closure library of
# every image on a Debian builder. The Dockerfile copies it from the
# build context named builder.
set -eu

# shellcheck source=../vulkan/closure.sh
. "$(dirname "$0")/closure.sh"

out=$1

# transect opens no file by name at run time except the CA bundle,
# which the trust image brings, so the binary is the one seed and the
# closure has no data files.
collect "$out" /transect ""

notices "$out" transect
