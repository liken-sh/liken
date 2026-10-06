#!/usr/bin/env bash
# Checks the indi-phd2 image before it ships. PHD2 must find every
# library it and its image loaders link, the packages must be the
# releases that package.toml states, and the image must hold the
# notices of the packages it took files from.
#
# PHD2 needs a Wayland compositor, and this check runs the image alone.
# So it starts PHD2 with no compositor and expects GTK's own failure to
# open the display: that proves PHD2 loaded and ran as far as the
# toolkit. The same start proves the entrypoint: it copies the mounted
# profile into $HOME and removes a stale instance lock, which a
# restarted container finds in its emptyDir.
#
# Usage: phd2.sh <image>
set -euo pipefail
# shellcheck source-path=SCRIPTDIR source=lib.sh
. "$(dirname "$0")/lib.sh"
image=$1

expect_upstream "$image"
expect_phd2 "$image"
expect_notices "$image" phd2

# The loader resolves every library of each program, as it would at
# the start, and prints "not found" for a library the image lacks.
for program in /usr/bin/phd2.bin /usr/libexec/glycin-loaders/2+/glycin-svg \
	/usr/libexec/glycin-loaders/2+/glycin-image-rs /usr/bin/bwrap; do
	if docker run --rm --entrypoint /usr/lib64/ld-linux-x86-64.so.2 "$image" --list "$program" |
		grep 'not found'; then
		echo "$program needs a library that the image does not hold" >&2
		exit 1
	fi
done

work=$(mktemp -d)
home=phd2-smoke-$$-$RANDOM
trap 'docker volume rm "$home" >/dev/null 2>&1 || true; rm -rf "$work"' EXIT
printf 'ConfigVersion=2001\n' >"$work/PHDGuidingV2"
chmod 644 "$work/PHDGuidingV2"
docker volume create "$home" >/dev/null

# The lock that a crashed PHD2 leaves names process 1. $HOME expands
# in the container, not here.
# shellcheck disable=SC2016
docker run --rm -v "$home:/home/phd2" --entrypoint /usr/bin/dash "$image" \
	-c 'echo 1 > "$HOME/phd2.1"'
out=$(docker run --rm -v "$home:/home/phd2" \
	-v "$work/PHDGuidingV2:/etc/phd2/PHDGuidingV2:ro" "$image" 2>&1 || true)
echo "$out"
grep -q 'Unable to initialize GTK' <<<"$out"
# shellcheck disable=SC2016
docker run --rm -v "$home:/home/phd2" --entrypoint /usr/bin/dash "$image" \
	-c '[ -f "$HOME/.PHDGuidingV2" ] && [ ! -e "$HOME/phd2.1" ]'
echo "PHD2 starts, and the entrypoint wrote the profile and removed the lock"
