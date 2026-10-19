#!/usr/bin/env bash
# Solves a synthetic star field with the astrometry image before it
# ships.
#
# Usage: astrometry.sh <image>
#
#   <image>  the full reference of the astrometry image, for example
#            ghcr.io/liken-sh/astrometry:20260928-1. The image must be
#            in the local Docker daemon.
#
# The check runs the three steps of a solve the way
# astrophotography-operator runs them: source-extractor writes a
# catalog of the stars in a frame, solve-field --just-augment turns the
# catalog into an .axy, and astrometry-engine solves the .axy against
# an index file. A missing library or data file shows up at the first
# run of each program, and the solve proves that the options the
# worker passes work on the release that the snapshot holds.
#
# The real index files are hundreds of megabytes, so the field is
# synthetic and its index file is small enough to commit. field.py
# says how both were made. The solve against real index files and a
# frame of the CCD simulator happens in the drill of
# astrophotography-operator's plan 08.
set -euo pipefail

image=$1
smoke=$(cd "$(dirname "$0")" && pwd)

# The image must hold the notices of the third-party software in it,
# as notices/check.sh describes.
"$smoke/../../notices/check.sh" "$image"

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
printf 'index /field/index.fits\n' > "$work/engine.cfg"

# The programs run as the caller, so the caller can delete what they
# write. /tmp in the image is writable by everyone.
run() {
  docker run --rm --user "$(id -u):$(id -g)" \
    -v "$smoke:/field:ro" -v "$work:/work" -w /work \
    "$image" "$@"
}

run /usr/bin/source-extractor /field/field.fits \
  -CATALOG_TYPE FITS_1.0 -CATALOG_NAME catalog.fits \
  -PARAMETERS_NAME /field/field.param -FILTER N -VERBOSE_TYPE QUIET

# Without --no-remove-lines and --uniformize 0, solve-field runs two
# Python modules, and the image holds no Python, so this step fails.
run /usr/bin/solve-field --just-augment --overwrite --no-plots \
  --no-remove-lines --uniformize 0 \
  --x-column X_IMAGE --y-column Y_IMAGE --sort-column FLUX_AUTO \
  --width 256 --height 256 --crpix-center \
  --scale-units arcsecperpix --scale-low 2.0 --scale-high 2.5 \
  -o field -D /work catalog.fits

# The engine writes field.wcs only when it solves the field.
run /usr/bin/astrometry-engine -c engine.cfg field.axy
if [ ! -f "$work/field.wcs" ]; then
  echo "astrometry-engine did not solve the synthetic field" >&2
  exit 1
fi

# field.py places the field's center at 150, 30 at 2.235 arcseconds per
# pixel. With --crpix-center the solution's reference point is the
# frame's center, so CRVAL1 and CRVAL2 must be that point, within one
# arcsecond, and the scale must be within 1%.
head -c 2880 "$work/field.wcs" | fold -w 80 > "$work/wcs.txt"
awk -F'[=/]' '
  $1 ~ /^CRVAL1 / { ra = $2 }
  $1 ~ /^CRVAL2 / { dec = $2 }
  $1 ~ /^CD1_1 / { cd11 = $2 }
  $1 ~ /^CD1_2 / { cd12 = $2 }
  END {
    scale = sqrt(cd11 * cd11 + cd12 * cd12) * 3600
    printf "solved: %.6f %.6f at %.4f arcseconds per pixel\n", ra, dec, scale
    d = 1 / 3600
    ok = (ra > 150 - d && ra < 150 + d && dec > 30 - d && dec < 30 + d && scale > 2.235 * 0.99 && scale < 2.235 * 1.01)
    exit !ok
  }' "$work/wcs.txt"
