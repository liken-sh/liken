#!/usr/bin/env bash
# Checks indi-qhy as drivers.sh checks every vendor image, and then the
# files that firmware-operator runs from it: fxload, the firmware, and
# the udev rules that observatory-operator writes its Firmware objects
# from. fxload runs as an upload pod runs it, as user 1000 on a
# read-only root with no capabilities and no network, and prints its
# version, so the program loads and needs nothing more to start.
#
# Usage: qhy.sh <image>, such as ghcr.io/liken-sh/indi-qhy:20261005-7
set -euo pipefail
image=$1

"$(dirname "$0")/drivers.sh" "$image"

docker run --rm --user 1000:1000 --read-only --cap-drop ALL --network none \
	--entrypoint fxload "$image" -V

name=$(docker create "$image" /none)
trap 'docker rm "$name" >/dev/null' EXIT
rules=$(docker cp "$name:/usr/lib/udev/rules.d/85-qhyccd.rules" - | tar -xO)
firmware=$(docker cp "$name:/usr/lib/firmware/qhy" - | tar -t | grep -c -E '\.(HEX|img)$')
if [ -z "$rules" ] || [ "$firmware" -eq 0 ]; then
	echo "$image holds no QHY rules or no QHY firmware" >&2
	exit 1
fi
echo "fxload runs as user 1000, and $firmware firmware files are in the image"
