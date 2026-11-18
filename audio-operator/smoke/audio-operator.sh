#!/usr/bin/env bash
# Checks that the audio-operator image contains every file that
# PipeWire and WirePlumber load at runtime, before the image ships.
#
# Usage: audio-operator.sh <image>
#
#   <image>  the full reference of the audio-operator image, for
#            example ghcr.io/liken-sh/audio-operator:2026.10.02-001.
#            The image must be in the local Docker daemon.
#
# The check reads /proc/<pid>/maps of the daemons with `sudo`, so the
# user that runs it needs `sudo` without a password.
#
# PipeWire and WirePlumber open their modules by file name, so ldd
# does not list them and go test does not cover them. This check
# starts both daemons, reads the files that the running processes
# mapped, and fails on any mapped file that is not in the image.
#
# The runner needs no sound card. PipeWire without a card still loads
# every module its configuration names, which are the modules this
# image exists to ship. The exception is `libspa-alsa.so`, which is
# loaded only when a card exists, so the check looks for it by name.
#
# The check also looks for these files by name, because no process in
# this run maps them:
#
# - The bluez5 plugin, its SBC codec, and its quirks database
#   (bluez-hardware.conf). WirePlumber here has no Bluetooth fragment
#   and no media bus, but in a pod that has both it opens all three.
# - `pw-top`, `pw-cli`, and `pw-metadata`, which a person runs with
#   `kubectl exec` while debugging.
# - `pw-record`, `flac`, `opusenc`, and `libstdbuf.so`, which the
#   capture container runs for each tap. `libstdbuf.so` is loaded
#   through `LD_PRELOAD`, so ldd does not find it either.
#
# The two daemons run in separate containers that share one volume, as
# in the pod. The image has no shell, so each container runs a binary
# directly.
set -euo pipefail
export LC_ALL=C

image=$1

# Fails when the image lacks the license notices of the third-party
# software in it. See notices/check.sh.
"$(dirname "$0")/../../notices/check.sh" "$image" \
  usr/share/doc/go/LICENSE
runtime=/var/run/audio.liken.sh

# Each container and the volume are named with this run's PID, so runs
# on one workstation do not collide, and the trap removes them if the
# script fails part way.
run="audio-smoke-$$"
work=$(mktemp -d)
# shellcheck disable=SC2329 # the EXIT trap below calls it
cleanup() {
  docker rm -f "$run-pipewire" "$run-wireplumber" "$run-probe" "$run-recorder" > /dev/null 2>&1 || true
  docker volume rm "$run" > /dev/null 2>&1 || true
  rm -rf "$work"
}
trap cleanup EXIT

# The path of every file in the image, as a process inside the
# container sees it.
container=$(docker create "$image")
docker export "$container" | tar -t | sed 's|^|/|; s|/$||' | sort -u > "$work/image-files.txt"
docker rm "$container" > /dev/null

for path in \
  /usr/bin/pipewire \
  /usr/bin/wireplumber \
  /usr/bin/pw-dump \
  /usr/bin/wpctl \
  /usr/bin/pw-top \
  /usr/bin/pw-cli \
  /usr/bin/pw-metadata \
  /usr/bin/pw-cat \
  /usr/bin/pw-record \
  /usr/bin/flac \
  /usr/bin/opusenc \
  /usr/libexec/coreutils/libstdbuf.so \
  /usr/lib/x86_64-linux-gnu/spa-0.2/alsa/libspa-alsa.so \
  /usr/lib/x86_64-linux-gnu/spa-0.2/bluez5/libspa-bluez5.so \
  /usr/lib/x86_64-linux-gnu/spa-0.2/bluez5/libspa-codec-bluez5-sbc.so \
  /usr/share/spa-0.2/bluez5/bluez-hardware.conf \
  /usr/share/wireplumber/scripts/liken/restore-device-ports.lua
do
  grep -qx "$path" "$work/image-files.txt" \
    || { echo "the image has no $path"; exit 1; }
done

docker volume create "$run" > /dev/null
# start NAME BINARY [ARGS...] runs BINARY from the image in a
# container named $run-NAME, with the shared runtime volume.
start() {
  name=$1
  shift
  docker run -d --name "$run-$name" -v "$run:$runtime" \
    -e PIPEWIRE_RUNTIME_DIR="$runtime" -e XDG_RUNTIME_DIR="$runtime" \
    --entrypoint "$1" "$image" "${@:2}" > /dev/null
}
# Runs one client to completion, and succeeds only if it exits 0
# within 20 seconds. A client that never returns is a failure this
# check must catch, so the wait has a limit.
probe() {
  docker rm -f "$run-probe" > /dev/null 2>&1 || true
  start probe "$@"
  code=$(timeout 20 docker wait "$run-probe" || echo unfinished)
  docker rm -f "$run-probe" > /dev/null
  [ "$code" = 0 ]
}
# Writes one `pw-dump` of the graph to the file in $1, for checks that
# read the graph instead of an exit status.
graph() {
  docker rm -f "$run-probe" > /dev/null 2>&1 || true
  start probe /usr/bin/pw-dump
  timeout 20 docker wait "$run-probe" > /dev/null || true
  docker logs "$run-probe" > "$1" 2> /dev/null || true
  docker rm -f "$run-probe" > /dev/null
}
# Runs a probe up to 10 times, 2 seconds apart, while a daemon starts,
# as the pod's startup probe does. A socket that no client can reach
# means the image is broken.
answers() {
  for _ in $(seq 10); do
    probe "$@" && return 0
    sleep 2
  done
  return 1
}

start pipewire /usr/bin/pipewire
answers /usr/bin/pw-dump \
  || { docker logs "$run-pipewire"; echo "pw-dump never connected"; exit 1; }
start wireplumber /usr/bin/wireplumber --profile=main-embedded
answers /usr/bin/wpctl status \
  || { docker logs "$run-wireplumber"; echo "wpctl never connected"; exit 1; }

# The file list above shows that these tools are in the image, and a
# run against the live graph shows that their libraries are too:
# `pw-top` needs `libncursesw` and `libtinfo`, and `pw-cli` needs
# `libreadline` and `libtinfo`. `pw-top -b` writes plain lines and
# calls no terminfo routine, so it needs no terminal or `TERM`, and
# `-n 1` makes it stop after one reading.
probe /usr/bin/pw-top -b -n 1 \
  || { echo "pw-top did not read the graph"; exit 1; }
probe /usr/bin/pw-cli info 0 \
  || { echo "pw-cli did not read the core"; exit 1; }
probe /usr/bin/pw-metadata -n settings \
  || { echo "pw-metadata did not read the settings"; exit 1; }

# The runner has no sound card, so the graph has no sink, and a
# capture stream with nothing to link to fails with "no target node
# available". The null sink from the support plugin needs no hardware
# and has monitor ports like any sink, so the recorder below taps it
# as the capture container taps a real sink. `object.linger` keeps the
# node after `pw-cli` exits.
probe /usr/bin/pw-cli create-node adapter \
  '{ factory.name=support.null-audio-sink node.name=gate-sink media.class=Audio/Sink object.linger=true audio.position=[FL FR] }' \
  || { docker logs "$run-pipewire"; echo "pw-cli did not create the null sink"; exit 1; }
for _ in $(seq 10); do
  graph "$work/graph.json"
  grep -q '"node.name": "gate-sink"' "$work/graph.json" && break
  sleep 1
done
grep -q '"node.name": "gate-sink"' "$work/graph.json" || {
  docker logs "$run-pipewire"
  echo "the null sink never appeared in the graph"
  exit 1
}

# `pw-record` opens `libsndfile` and the PipeWire client libraries,
# and each encoder opens its own codec library, so a run shows a
# library that the closure missed, through the dynamic loader's error
# on stderr.
#
# The `-P` argument is the properties object that the capture
# container builds: the node name that the container uses to confirm
# the tap, and `stream.capture.sink`, which selects a sink's monitor
# ports. If `pw-cat` rejected that object, the container could never
# confirm a tap, and only the graph shows that, so the check looks for
# a node of that name in the graph while the recorder runs.
docker run -d --name "$run-recorder" -v "$run:$runtime" \
  -e PIPEWIRE_RUNTIME_DIR="$runtime" -e XDG_RUNTIME_DIR="$runtime" \
  --entrypoint /usr/bin/pw-record "$image" \
  -P '{ node.name = "audio-capture-release", stream.capture.sink = true }' \
  --target gate-sink \
  --raw --format s16 --rate 48000 --channels 2 - > /dev/null
found=no
for _ in $(seq 10); do
  sleep 1
  graph "$work/graph.json"
  if grep -q '"node.name": "audio-capture-release"' "$work/graph.json"; then
    found=yes
    break
  fi
done
docker logs "$run-recorder" > /dev/null 2> "$work/pw-record.err" || true
docker rm -f "$run-recorder" > /dev/null
if grep -qi 'error while loading shared libraries' "$work/pw-record.err"; then
  cat "$work/pw-record.err"
  echo "pw-record is missing a library"
  exit 1
fi
[ "$found" = yes ] || {
  cat "$work/pw-record.err"
  echo "pw-record did not take the properties object the capture container builds"
  exit 1
}
for encoder in \
  "/usr/bin/flac --force-raw-format --endian=little --sign=signed --bps=16 --channels=2 --sample-rate=48000 --stdout -" \
  "/usr/bin/opusenc --raw --raw-bits 16 --raw-rate 48000 --raw-chan 2 - -"
do
  # shellcheck disable=SC2086
  set -- $encoder
  head -c 19200 /dev/zero | docker run --rm -i \
    -e LD_PRELOAD=/usr/libexec/coreutils/libstdbuf.so -e _STDBUF_O=0 \
    --entrypoint "$1" "$image" "${@:2}" > /dev/null \
    || { echo "$1 did not encode"; exit 1; }
done

# `wpctl` connects to PipeWire, not to WirePlumber, so it succeeds
# even when WirePlumber has exited, and the daemons' maps are read
# only after this check that both still run.
for name in pipewire wireplumber; do
  [ "$(docker inspect -f '{{.State.Running}}' "$run-$name")" = true ] \
    || { docker logs "$run-$name"; echo "$name is not running"; exit 1; }
done

# /proc/<pid>/maps shows paths as the process sees them, so they match
# the image's file list. The filter drops the shared volume and the
# kernel's pseudo-filesystems, which are not in the image. grep exits 1
# when it selects no line, and pipefail would then end the script
# before the "mapped nothing" message below, so the filter accepts
# that exit.
missing=0
for name in pipewire wireplumber; do
  pid=$(docker inspect -f '{{.State.Pid}}' "$run-$name")
  sudo awk '$6 ~ /^\// { print $6 }' "/proc/$pid/maps" \
    | { grep -Ev "^/memfd:|^/(dev|proc|sys)/|^$runtime/" || true; } \
    | sort -u > "$work/maps-$name.txt"
  echo "$name mapped $(wc -l < "$work/maps-$name.txt") files"
  [ -s "$work/maps-$name.txt" ] || { echo "$name mapped nothing"; exit 1; }
  if comm -23 "$work/maps-$name.txt" "$work/image-files.txt" | grep .; then
    echo "$name mapped files the image does not contain"
    missing=1
  fi
done
exit "$missing"
