#!/bin/sh
# Copies transect and every shared library that the dynamic loader
# resolves for it into one directory, which the image copies onto
# scratch.  Run it in a builder that has the binary at /transect, with
# the output directory as the argument.
#
# collect and notices come from vulkan/closure.sh, which the Dockerfile
# copies in from the build context named builder.
set -eu

# shellcheck source=../vulkan/closure.sh
. "$(dirname "$0")/closure.sh"

out=$1

# At run time transect opens no file of this tree except its
# libraries.  The CA bundle comes from the trust image, and the API
# server's credentials from the ServiceAccount mount.  So the binary is
# the only starting point of the library walk, and the closure has no
# data files.
collect "$out" /transect ""

notices "$out" transect
