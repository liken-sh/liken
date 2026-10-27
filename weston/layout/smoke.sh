#!/usr/bin/env bash
# Runs liken-layout.so against a real weston and real clients, on a
# developer machine with docker. CI does not run this: it needs a
# container that can start a second container, and the release image
# has no shell and no test clients in it.
#
# The test builds a small image with smoke-image.sh, from the same
# Debian image and the same snapshot date as the release, with weston
# and its demo clients in it, starts weston headless with the pixman
# renderer, then drives the control socket from a Python client. The
# module builds against the same libweston as the one that ships.
# LIKEN_SMOKE_CFLAGS passes extra compiler flags to the module's build.
#
# It asserts that a client on a per-claim socket is reported with that
# socket's name and a client on the shared socket as wayland-0. It
# asserts that placing each one paints the rectangle the operator
# asked for and nothing outside it, that moving one leaves the
# rectangle it came from black, that hiding one leaves the other where
# it was, and that a hide with a fade leaves the rectangle painted and
# dimmer partway through the fade and black after it. It asserts that
# closing the claim's socket unlinks the path, and that a listen on
# that same name afterwards opens the path again, leaves weston with
# one listener on the name, and reports the client that arrives on it. It asserts that a client on the capture
# socket reads complete, and that a client on wayland-0 reads failed
# with weston's own word, unauthorized.
#
# With the argument arrange, the script runs arrange_smoke.py against
# the same compositor instead. That test asserts the
# liken_arrangement_v1 protocol: its header says what.
#
# weston runs with no --debug, because --debug installs
# screenshot_allow_all, which would admit the wayland-0 client.
#
# With LIKEN_SMOKE_CFLAGS=-fsanitize=address, the module and the drill
# build with AddressSanitizer, weston runs with its runtime preloaded,
# and the test fails when the sanitizer reports an error in weston's log.
set -euo pipefail

here=$(cd "$(dirname "$0")" && pwd)
image=liken-layout-smoke
weston_container=liken-layout-smoke-weston
client_container=liken-layout-smoke-client
work=$(mktemp -d)

case "${1:-}" in
"") driver=smoke_client.py ;;
arrange) driver=arrange_smoke.py ;;
*) echo "usage: smoke.sh [arrange]" >&2; exit 2 ;;
esac

# Every container the tests start has a name that begins with
# liken-layout-smoke-.
remove_containers() {
	docker ps -aq --filter name=liken-layout-smoke- | xargs -r docker rm -f >/dev/null 2>&1 || true
}

# shellcheck disable=SC2329  # the EXIT trap below is the only caller.
cleanup() {
	remove_containers
	rm -rf "$work"
}
trap cleanup EXIT

remove_containers

# shellcheck disable=SC2086  # the flags are several words or none.
bash "$here/smoke-image.sh" "$image" ${LIKEN_SMOKE_CFLAGS:-}

# The compositor runs as the developer's own uid, so the sockets and
# the screenshot it writes are readable from the host.
mkdir -m 700 "$work/run"
mkdir -m 755 "$work/control" "$work/shot" "$work/weston-config"
cat >"$work/weston.ini" <<'INI'
[core]
backend=headless
idle-time=0
require-input=false

[output]
name=headless
mode=1280x720
INI

# A module built with a sanitizer needs the sanitizer's runtime loaded
# before it, and weston is not built with one, so the runtime is
# preloaded. Leak checks are off: weston frees little at exit.
sanitizer=()
case "${LIKEN_SMOKE_CFLAGS:-}" in
*sanitize=address*)
	asan=$(docker run --rm "$image" sed -n 's/^LIKEN_ASAN=//p' /etc/liken-asan)
	sanitizer=(-e "LD_PRELOAD=$asan" -e ASAN_OPTIONS=detect_leaks=0)
	;;
esac

echo "== starting weston"
docker run -d --name "$weston_container" \
	"${sanitizer[@]}" \
	--user "$(id -u):$(id -g)" \
	-v "$work/run:/run/liken" \
	-v "$work/control:/run/control" \
	-v "$work/shot:/shot" \
	-v "$work/weston-config:/etc/weston" \
	-v "$work/weston.ini:/etc/weston.ini:ro" \
	-e XDG_RUNTIME_DIR=/run/liken \
	-e LIKEN_LAYOUT_SOCKET=/run/control/layout.sock \
	"$image" \
	weston --config=/etc/weston.ini --backend=headless \
	--shell=ivi-shell.so --modules=liken-layout.so \
	--renderer=pixman --socket=wayland-0 >/dev/null

echo "== driving the control socket"
set +e
WORK=$work IMAGE=$image WESTON=$weston_container CLIENT=$client_container \
	python3 -B "$here/$driver"
status=$?
set -e

echo "== weston log"
docker logs "$weston_container" 2>&1 | sed 's/^/  /'

if docker logs "$weston_container" 2>&1 | grep -q 'ERROR: AddressSanitizer'; then
	echo "   FAIL: AddressSanitizer reported an error in weston"
	status=1
fi

exit $status
