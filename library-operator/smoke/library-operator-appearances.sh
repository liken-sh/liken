#!/usr/bin/env bash
# Proves the library-operator-appearances image before it ships.
#
# Usage: library-operator-appearances.sh <image>
#
#   <image>  the full reference of the appearances image, for example
#            ghcr.io/liken-sh/library-operator-appearances:2026.10.02-001.
#            The image must be in the local Docker daemon.
#
# The tool opens OpenVINO, its plugins, and the models when it runs,
# not when it starts, so only a run of the passes proves them. The
# image's own ffmpeg makes a short test video with no faces in a movie
# folder, and the worker runs on it the way the worker Job runs: the
# pod program reads a work list, runs detect and match, and writes the
# ledger. --device auto loads the GPU plugin and Intel's OpenCL runtime
# to look for a GPU. The runner has none, so the models run on the CPU,
# and the GPU's compiler is proved on hardware.
set -euo pipefail

image=$1

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

# The containers run as this user, so the files they write in the work
# directory are this user's to delete.
run() {
  docker run --rm --user "$(id -u):$(id -g)" --volume "$work:/library" "$@"
}

title="Test Pattern (2026)"
video="$title/$title.mkv"
mkdir -p "$work/$title/.liken" "$work/.contributors/te/test-actor" "$work/.liken/worklists"

run --entrypoint /usr/bin/ffmpeg "$image" -hide_banner -loglevel error \
  -f lavfi -i testsrc2=duration=4:size=640x360:rate=24 \
  -c:v mpeg4 -g 24 "/library/$video"

# The tool alone, as a person runs it from the image.
run "$image" detect --device auto "/library/$video"
test -s "$work/$title/.liken/appearances/$title.mkv.jsonl"

# The worker. The record goes first, so the worker runs detect itself. The
# cast is one actor with no headshot, so the match names no face and
# lists the actor as unmatched, and the attempt is found.
rm "$work/$title/.liken/appearances/$title.mkv.jsonl"
cat > "$work/$title/.liken/credits.yaml" <<EOF
credits:
    - name: Test Actor
      part: actor
      order: 0
      contributor: .contributors/te/test-actor
EOF
size=$(stat -c %s "$work/$video")
printf '{"path":"%s","size":%d,"durationMs":4000,"listed":"2026-01-01T00:00:00Z"}\n' \
  "$video" "$size" > "$work/.liken/worklists/appearances.jsonl"
run --entrypoint /library-operator-pod \
  --env LIBRARY_FACT=appearances --env LIBRARY_KIND=movies --env LIBRARY_ROOT= \
  --env JOB_NAME=smoke "$image" worker
ledger="$work/$title/.liken/appearances.yaml"
grep -q 'result: found' "$ledger"
grep -q 'headshot: missing' "$ledger"
