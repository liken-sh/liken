#!/usr/bin/env bash
# Takes one frame with the CCD simulator pointed at M42 and counts the
# stars in it. The simulator draws catalog stars through gsc, which it
# runs with popen and /bin/sh, so an empty frame means the catalog, the
# program, or the shell is missing.
#
# Usage: simulators.sh <image>
set -euo pipefail
# shellcheck source-path=SCRIPTDIR source=lib.sh
. "$(dirname "$0")/lib.sh"
image=$1

expect_upstream "$image"
name=$(serve "$image" indi_simulator_telescope indi_simulator_ccd)
trap 'stop "$name"' EXIT
sleep 2
setprop "$name" "Telescope Simulator.CONNECTION.CONNECT=On" "CCD Simulator.CONNECTION.CONNECT=On"
sleep 2
# The simulator draws an empty frame at its default focal length of 0.
setprop "$name" "CCD Simulator.SCOPE_INFO.FOCAL_LENGTH;APERTURE=500;80"
setprop "$name" "CCD Simulator.UPLOAD_SETTINGS.UPLOAD_DIR;UPLOAD_PREFIX=/tmp;IMAGE_XXX"
setprop "$name" "CCD Simulator.UPLOAD_MODE.UPLOAD_LOCAL=On"
setprop "$name" "Telescope Simulator.TELESCOPE_PARK.UNPARK=On"
setprop "$name" "Telescope Simulator.ON_COORD_SET.TRACK=On"
setprop "$name" "Telescope Simulator.EQUATORIAL_EOD_COORD.RA;DEC=5.588;-5.39"
for _ in $(seq 30); do
	sleep 1
	if [ "$(getprop "$name" -1 'Telescope Simulator.EQUATORIAL_EOD_COORD._STATE')" = Ok ]; then
		break
	fi
done
setprop "$name" "CCD Simulator.CCD_EXPOSURE.CCD_EXPOSURE_VALUE=2"
sleep 6
frame=$(mktemp)
docker cp "$name:/tmp/IMAGE_001.fits" "$frame"
python3 "$here/stars.py" "$frame"
rm -f "$frame"
