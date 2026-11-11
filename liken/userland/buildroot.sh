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

# buildroot's recipe of a package holds patches for the version that
# the recipe names. A version that versions.mk sets is a later
# release, which can already hold the change of some of them, and a
# patch that does not apply stops the build. The file
# patches/<package>/<version>/held names each patch of buildroot's
# recipe that the release already holds, one file name to a line.
#
# buildroot reads a package's patches from the directory named for
# the package's version when the recipe's directory has one, and from
# the recipe's directory when it does not. So for each held file the
# loop makes that directory beside the recipe and copies into it each
# patch of the recipe that the file does not name. buildroot then
# applies those, and none of the held ones. A package with no held
# file takes every patch of its recipe, as zic does.
#
# A name in a held file that buildroot's recipe does not have stops
# the build, so the list never names a patch that buildroot dropped.
for held in "$here"/patches/*/*/held; do
    [[ -f "$held" ]] || continue
    version="$(basename "$(dirname "$held")")"
    package="$(basename "$(dirname "$(dirname "$held")")")"
    recipe="$tree/package/$package"
    mapfile -t names < <(grep -v '^#' "$held" | grep .)
    for name in "${names[@]}"; do
        [[ -f "$recipe/$name" ]] || {
            echo "buildroot.sh: patches/$package/$version/held names $name, and buildroot's recipe of $package has no such patch" >&2
            exit 1
        }
    done
    rm -rf "${recipe:?}/$version"
    mkdir -p "$recipe/$version"
    for patch in "$recipe"/*.patch; do
        [[ -f "$patch" ]] || continue
        if ! printf '%s\n' "${names[@]}" | grep -qxF "$(basename "$patch")"; then
            cp "$patch" "$recipe/$version/"
        fi
    done
done

make -C "$tree" O="$output" BR2_EXTERNAL="$here" BR2_DEFCONFIG="$output/liken_defconfig" defconfig
make -C "$tree" O="$output"
make -C "$tree" O="$output" legal-info
