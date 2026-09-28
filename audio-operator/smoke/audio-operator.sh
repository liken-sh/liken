#!/usr/bin/env bash
# Proves the closure of the audio-operator image before it ships.
#
# Usage: audio-operator.sh <image>
#
#   <image>  the full reference of the audio-operator image, for
#            example ghcr.io/liken-sh/audio-operator:2026.10.02-001.
#            The image must be in the local Docker daemon.
#
# The check reads /proc/<pid>/maps of the daemons with `sudo`, so the
# user that runs it needs `sudo` with no password.
#
# This check makes a missing runtime load impossible to ship. PipeWire
# and WirePlumber open every module by file name, so `ldd` reports
# none of them and `go test` covers none of them either. This check
# starts both daemons, reads what the running processes mapped, and
# fails on any mapped file the image does not contain.
#
# The runner has no sound card and needs none. A cardless PipeWire
# still loads every module its configuration names, and that is the set
# this image exists to ship. `libspa-alsa.so` is the one load no
# cardless run reaches, so the check asserts it by name instead.
#
# The bluez5 plugin, its SBC codec, and its quirks database are
# asserted by name for the same reason. This run's WirePlumber has no
# Bluetooth fragment and no media bus, so it opens none of them, and a
# pod that has both opens all three.
#
# `pw-top`, `pw-cli`, and `pw-metadata` are asserted by name too. No
# daemon maps them, because a person runs each one with `kubectl exec`
# during a debugging session, so the map check reports nothing when one
# drops out of the closure.
#
# `pw-record`, `flac`, `opusenc`, and `libstdbuf.so` are asserted by
# name for the same reason: the capture container runs each one per
# tap, no daemon in this check maps any of them, and `libstdbuf.so`
# arrives through `LD_PRELOAD` rather than through any library graph
# `ldd` walks.
#
# The two daemons run as separate containers sharing one volume, the
# same shape the pod uses. The image has no shell, so every container
# names a binary directly.
set -euo pipefail
export LC_ALL=C

image=$1
runtime=/var/run/audio.liken.sh

# Every container and the volume carry this run's name, so a run on a
# workstation does not collide with other containers, and the trap
# removes them when a check fails part way.
run="audio-smoke-$$"
work=$(mktemp -d)
# shellcheck disable=SC2329 # the EXIT trap below calls it
cleanup() {
  docker rm -f "$run-pipewire" "$run-wireplumber" "$run-probe" "$run-recorder" > /dev/null 2>&1 || true
  docker volume rm "$run" > /dev/null 2>&1 || true
  rm -rf "$work"
}
trap cleanup EXIT

# Every path in the image, as the paths a process sees.
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
  /usr/share/spa-0.2/bluez5/bluez-hardware.conf
do
  grep -qx "$path" "$work/image-files.txt" \
    || { echo "the image has no $path"; exit 1; }
done

docker volume create "$run" > /dev/null
# A container of this image, named for the binary it runs.
start() {
  name=$1
  shift
  docker run -d --name "$run-$name" -v "$run:$runtime" \
    -e PIPEWIRE_RUNTIME_DIR="$runtime" -e XDG_RUNTIME_DIR="$runtime" \
    --entrypoint "$1" "$image" "${@:2}" > /dev/null
}
# One client run, to completion, under a bound. A client that never
# returns is the failure this check exists to catch, so the wait for it
# cannot be unbounded.
probe() {
  docker rm -f "$run-probe" > /dev/null 2>&1 || true
  start probe "$@"
  code=$(timeout 20 docker wait "$run-probe" || echo unfinished)
  docker rm -f "$run-probe" > /dev/null
  [ "$code" = 0 ]
}
# One `pw-dump` into a file, for the checks that read the graph rather
# than an exit status.
graph() {
  docker rm -f "$run-probe" > /dev/null 2>&1 || true
  start probe /usr/bin/pw-dump
  timeout 20 docker wait "$run-probe" > /dev/null || true
  docker logs "$run-probe" > "$1" 2> /dev/null || true
  docker rm -f "$run-probe" > /dev/null
}
# The probe the pod's startupProbe runs. A socket nobody can reach is a
# broken image.
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

# Each of the three runs once against the live graph. The listing above
# proves the binary is in the image, and this proves every library it
# opens is there too: `pw-top` needs `libncursesw` and `libtinfo`, and
# `pw-cli` needs `libreadline` over the same `libtinfo`. `pw-top`'s
# `-b` writes plain lines and calls no terminfo routine, so it runs
# with no terminal and no `TERM`, and `-n 1` stops it after one
# reading.
probe /usr/bin/pw-top -b -n 1 \
  || { echo "pw-top did not read the graph"; exit 1; }
probe /usr/bin/pw-cli info 0 \
  || { echo "pw-cli did not read the core"; exit 1; }
probe /usr/bin/pw-metadata -n settings \
  || { echo "pw-metadata did not read the settings"; exit 1; }

# A sink for the recorder below to tap. The runner has no sound card,
# so the graph has no sink at all and a capture stream with nowhere to
# link fails with "no target node available" before it ever settles
# into the graph. A null sink is a node the support plugin builds in
# software, needs no hardware, and carries monitor ports like any other
# sink, so the tap below is the tap the capture container makes.
# `object.linger` keeps the node after `pw-cli` exits.
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

# The listing above proves the files exist. This proves they load:
# `pw-record` opens `libsndfile` and the PipeWire client libraries,
# and each encoder opens its own codec library, so a run of each one
# against the live graph is what reports a library the closure lost.
# The loader's own complaint on stderr is one signal.
#
# The `-P` line is the one the capture container builds: a SPA JSON
# object carrying the name the confirmation matches on and, for a sink,
# PipeWire's own monitor key. A `pw-cat` that refused that object would
# leave every tap unconfirmed, and the graph is the only place that
# shows, so the graph is read while the recorder still runs and asked
# whether the node arrived under its name.
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

# `wpctl` connects to PipeWire, not to WirePlumber, so it answers even
# when the session manager has left. Both daemons must still be up
# before their maps mean anything.
for name in pipewire wireplumber; do
  [ "$(docker inspect -f '{{.State.Running}}' "$run-$name")" = true ] \
    || { docker logs "$run-$name"; echo "$name is not running"; exit 1; }
done

# What the two live processes mapped. The kernel writes the paths as
# the process sees them, so they compare against the listing above.
# The volume and the kernel's own filesystems are not the image. grep
# exits 1 when it selects no line, and pipefail would then end the
# check before the "mapped nothing" message below, so the filter
# accepts that exit.
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
