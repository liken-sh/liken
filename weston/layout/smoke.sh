#!/usr/bin/env bash
# Runs liken-layout.so against a real weston and real clients, on a
# developer machine with docker. CI does not run this: it needs a
# container that can start a second container, and the release image
# has no shell and no test clients in it.
#
# The test builds a small image from the same Debian image and the
# same snapshot date as the release, with weston and its demo clients
# in it, starts weston headless with the pixman renderer, then drives
# the control socket from a Python client. The script reads the image
# from the layout stage of weston/Dockerfile and the date from
# weston/package.toml, so the module builds against the same libweston
# as the one that ships.
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
# weston runs with no --debug, because --debug installs
# screenshot_allow_all, which would admit the wayland-0 client.
set -euo pipefail

here=$(cd "$(dirname "$0")" && pwd)
image=liken-layout-smoke
weston_container=liken-layout-smoke-weston
client_container=liken-layout-smoke-client
work=$(mktemp -d)

# shellcheck disable=SC2329  # the EXIT trap below is the only caller.
cleanup() {
	docker rm -f "$weston_container" "$client_container" \
		"$client_container-shared" "$client_container-again" \
		>/dev/null 2>&1 || true
	rm -rf "$work"
}
trap cleanup EXIT

docker rm -f "$weston_container" "$client_container" \
	"$client_container-shared" "$client_container-again" >/dev/null 2>&1 || true

base=$(sed -n 's/^FROM \(debian:[^ ]*\) AS layout$/\1/p' "$here/../Dockerfile")
snapshot=$(sed -n 's/^version = "\([0-9]*\)"$/\1/p' "$here/../package.toml")
: "${base:?weston/Dockerfile names no debian image for the layout stage}"
: "${snapshot:?weston/package.toml names no snapshot date}"

echo "== building $image on $base at snapshot $snapshot"
docker build -q -t "$image" -f - \
	--build-arg BASE="$base" --build-arg VERSION="$snapshot" \
	--build-context builder="$here/../../vulkan" \
	"$here" >/dev/null <<'DOCKERFILE'
ARG BASE
FROM $BASE
ARG VERSION
COPY --from=builder snapshot.sh snapshot.sha256 /
RUN sh /snapshot.sh "$VERSION" \
    && apt-get upgrade -y \
    && apt-get install -y --no-install-recommends \
        weston libweston-16-dev gcc libc6-dev pkg-config \
        libwayland-dev libpixman-1-dev \
    && rm -rf /var/lib/apt/lists/*
COPY liken-layout.c ivi-layout-export.h /build/
RUN gcc -Wall -Wextra -Werror -shared -fPIC \
        $(pkg-config --cflags libweston-16 wayland-server pixman-1) \
        -o /usr/lib/x86_64-linux-gnu/weston/liken-layout.so /build/liken-layout.c
DOCKERFILE

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

echo "== starting weston"
docker run -d --name "$weston_container" \
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
	python3 "$here/smoke_client.py"
status=$?
set -e

echo "== weston log"
docker logs "$weston_container" 2>&1 | sed 's/^/  /'

exit $status
