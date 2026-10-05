#!/usr/bin/env bash
# Starts the indi image once before it ships. Every core driver must
# load in the image and stay up, the packages must be the releases
# that package.toml states, and the simulators must serve a mount that
# a client connects and slews. The shim must refuse a name with no
# address, which proves it runs in the image.
#
# Usage: indi.sh <image>
set -euo pipefail
. "$(dirname "$0")/lib.sh"
image=$1

expect_upstream "$image"

# indi-bin also installs its tools, such as indi_getprop, which are not
# drivers.
id=$(docker create "$image")
files=$(docker export "$id" | tar -t)
docker rm "$id" >/dev/null
drivers=$(sed -n 's|^usr/bin/\(indi_[^/]*\)$|\1|p' <<<"$files" |
	grep -vE '^indi_(getprop|setprop|eval|getdevice|hid_test)$')
name=$(serve "$image" $drivers)
trap 'stop "$name"' EXIT
expect_clean_log "$name"
echo "$(wc -w <<<"$drivers") drivers loaded"

stop "$name"
name=$(serve "$image" indi_simulator_telescope)
sleep 2
setprop "$name" "Telescope Simulator.CONNECTION.CONNECT=On"
sleep 1
setprop "$name" "Telescope Simulator.TELESCOPE_PARK.UNPARK=On"
setprop "$name" "Telescope Simulator.ON_COORD_SET.TRACK=On"
setprop "$name" "Telescope Simulator.EQUATORIAL_EOD_COORD.RA;DEC=5.588;-5.39"
for _ in $(seq 30); do
	sleep 1
	if [ "$(getprop "$name" -1 'Telescope Simulator.EQUATORIAL_EOD_COORD._STATE')" = Ok ]; then
		break
	fi
done
getprop "$name" 'Telescope Simulator.EQUATORIAL_EOD_COORD.*'
getprop "$name" -1 'Telescope Simulator.EQUATORIAL_EOD_COORD._STATE' | grep -qx Ok

if out=$(docker run --rm --entrypoint /usr/bin/indi-shim "$image" 2>&1); then
	echo "the shim ran with no address" >&2
	exit 1
fi
grep -q 'host:port' <<<"$out"
