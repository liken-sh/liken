#!/usr/bin/env bash
# Starts every driver of one image on the indi image under indiserver,
# with no hardware attached. Each must load and stay up. A camera
# driver defines no device until it finds a camera, so this proves the
# closure and not the camera code.
#
# Usage: drivers.sh <image>, such as ghcr.io/liken-sh/indi-zwo:20261005-1
set -euo pipefail
# shellcheck source-path=SCRIPTDIR source=lib.sh
. "$(dirname "$0")/lib.sh"
image=$1
list=${image##*/}
list=${list%%:*}
list=${list#indi-}

expect_upstream "$image"
mapfile -t drivers < <(drivers_of "$list")
name=$(serve "$image" "${drivers[@]}")
trap 'stop "$name"' EXIT
expect_clean_log "$name"
echo "${#drivers[@]} drivers of $list loaded"
