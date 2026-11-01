#!/usr/bin/env bash
#
# Run one buildroot build of this BR2_EXTERNAL tree, inside the
# `buildroot` image of toolchains/images.toml.
#
# Two builds read this tree: build.sh builds the static programs of the
# OS from os.config, and bluetooth-operator/bluetoothd/Dockerfile
# builds the daemons of the bluetoothd image from bluetoothd.config.
# Both run this script, so both expand the configuration and run
# buildroot the same way.
#
# The script writes common.config and the named fragments into one
# defconfig, because buildroot reads one. `make defconfig` expands it,
# the plain `make` builds every package, and `legal-info` copies the
# license files and the source tarballs of every package into
# <output>/legal-info/.
#
# Usage:
#   buildroot.sh <buildroot tree> <output directory> <fragment>...
#
# The caller sets BR2_DL_DIR, the download directory, and
# BR2_CCACHE_DIR, the compiler cache.

set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
(($# >= 3)) || {
    echo "usage: buildroot.sh <buildroot tree> <output directory> <fragment>..." >&2
    exit 2
}
tree="$1" output="$2"
shift 2

mkdir -p "$output"
{
    cat "$here/common.config"
    for fragment in "$@"; do
        cat "$here/$fragment"
    done
} >"$output/liken_defconfig"

make -C "$tree" O="$output" BR2_EXTERNAL="$here" BR2_DEFCONFIG="$output/liken_defconfig" defconfig
make -C "$tree" O="$output"
make -C "$tree" O="$output" legal-info
