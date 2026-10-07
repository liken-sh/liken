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
# pod program reads its gap from the catalog, runs detect and match, and
# writes the ledger. A worker pod reads the gap through its own catalog
# agent, and this script stands in for the agent with a server that
# answers every query with the one video, because the agent is proved by
# its own image's smoke test, and the gap query by the Go tests. --device auto loads the GPU plugin and Intel's OpenCL runtime
# to look for a GPU. The runner has none, so the models run on the CPU,
# and the GPU's compiler is proved on hardware.
set -euo pipefail

image=$1

# The image must hold the notices of the third-party software in it,
# as notices/check.sh describes.
"$(dirname "$0")/../../notices/check.sh" "$image" \
  usr/share/doc/go/LICENSE \
  usr/share/doc/rust/COPYRIGHT-library.html \
  usr/share/doc/openvino/EULA.txt \
  usr/share/doc/intel-graphics-compiler/NOTICES.txt

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

# The containers run as this user, so the files they write in the work
# directory are this user's to delete.
run() {
  docker run --rm --user "$(id -u):$(id -g)" --volume "$work:/library" "$@"
}

title="Test Pattern (2026)"
video="$title/$title.mkv"
mkdir -p "$work/$title/.liken" "$work/.contributors/te/test-actor"

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

# The stand-in catalog: one row of the gap, in the agent's stream of
# events, for every query. The worker sends one query, the gap, because
# it waits for no sync target.
port=18431
python3 - "$port" "$video" "$size" <<'PY' &
import http.server, json, sys
port, path, size = int(sys.argv[1]), sys.argv[2], int(sys.argv[3])
body = "\n".join(json.dumps(event) for event in [
    {"columns": ["path", "size", "duration"]},
    {"row": [1, [path, size, 4000]]},
    {"eoq": {"time": 0}},
]).encode() + b"\n"
class Catalog(http.server.BaseHTTPRequestHandler):
    def do_POST(self):
        self.rfile.read(int(self.headers.get("Content-Length", 0)))
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.end_headers()
        self.wfile.write(body)
    def log_message(self, *args):
        pass
http.server.HTTPServer(("127.0.0.1", port), Catalog).serve_forever()
PY
catalog=$!
trap 'kill "$catalog" 2>/dev/null; rm -rf "$work"' EXIT
for _ in $(seq 50); do
  curl -s -o /dev/null -X POST "http://127.0.0.1:$port/" && break
  sleep 0.1
done

run --network host --entrypoint /library-operator-pod \
  --env LIBRARY_FACT=appearances --env LIBRARY_KIND=movies --env LIBRARY_ROOT= \
  --env LIBRARY_CATALOG_API="http://127.0.0.1:$port" \
  --env JOB_NAME=smoke "$image" worker
ledger="$work/$title/.liken/appearances.yaml"
grep -q 'result: found' "$ledger"
grep -q 'headshot: missing' "$ledger"
