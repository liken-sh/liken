#!/usr/bin/env bash
# The functions the INDI smoke checks share. Each image is a closure on
# scratch with no shell, so a check drives the image's own programs
# from outside: indiserver as the entrypoint, indi_getprop and
# indi_setprop through docker exec, and docker cp to read a file.

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
indi=$(dirname "$here")

# serve starts indiserver in the image with the drivers named, and
# prints the container's name. -r 0 makes a driver that dies stay
# dead, so the log shows it once.
serve() {
	local image=$1
	shift
	local name
	name=indi-smoke-$$-$RANDOM
	docker run -d --name "$name" "$image" -r 0 "$@" >/dev/null
	echo "$name"
}

# stop removes a container that serve started.
stop() { docker rm -f "$1" >/dev/null 2>&1 || true; }

# getprop and setprop run INDI's own clients inside the container.
getprop() {
	local name=$1
	shift
	docker exec "$name" /usr/bin/indi_getprop -t 5 "$@"
}
setprop() {
	local name=$1
	shift
	docker exec "$name" /usr/bin/indi_setprop -t 5 "$@"
}

# drivers_of prints the drivers that a list in images/ names.
drivers_of() { awk '$1 == "driver" {print $2}' "$indi/images/$1"; }

# expect_clean_log fails when a driver of the container failed to load
# a library or died, after the drivers had a few seconds to start.
expect_clean_log() {
	local name=$1 log
	sleep 5
	log=$(docker logs "$name" 2>&1)
	if [ "$(docker inspect -f '{{.State.Running}}' "$name")" != true ] ||
		grep -qE 'error while loading shared libraries|cannot open shared object|Terminated after|read EOF' <<<"$log"; then
		echo "$log" | tail -n 40
		echo "a driver failed to load or died" >&2
		return 1
	fi
}

# expect_upstream checks that the packages in the image are the
# upstream releases that package.toml states.
expect_upstream() {
	local image=$1 name versions
	name=$(docker create "$image")
	versions=$(docker cp "$name:/etc/indi-versions" - | tar -xO)
	docker rm "$name" >/dev/null
	local pkg upstream
	for pair in "indi-bin indi" "indi-3rdparty-drivers indi-3rdparty" "gsc gsc"; do
		pkg=${pair% *}
		upstream=$(sed -n "s/^${pair#* } = \"\(.*\)\"$/\1/p" "$indi/package.toml")
		if ! grep -qx "$pkg	$upstream" <<<"$versions"; then
			echo "the image holds:" >&2
			echo "$versions" >&2
			echo "package.toml states ${pair#* } = \"$upstream\"" >&2
			return 1
		fi
	done
}

# expect_notices checks that the image holds the copyright file of each
# package it took files from, the Go license of the shim, and the
# license file of each vendor SDK that its list in images/ names.
expect_notices() {
	local image=$1 list=$2
	local licenses
	mapfile -t licenses < <(awk '$1 == "license" {print "usr/share/doc/indi-3rdparty-libs/" $2}' "$indi/images/$list")
	"$indi/../notices/check.sh" "$image" usr/share/doc/go/LICENSE "${licenses[@]}"
}
