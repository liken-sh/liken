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
# folder, and the worker runs on it the way a pod of the worker Job runs:
# the pod program reads its one video from the bus, runs detect and
# match, and writes the ledger. A worker pod reads the retained message
# at its index from the broker, and this script stands in for the broker
# with a server that holds that one message, because the broker is
# upstream's, and the session the pod speaks is proved by the Go tests
# and on a test cluster. --device auto loads the GPU plugin and Intel's OpenCL runtime
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

# The stand-in broker: it accepts the pod's connection, answers its
# subscription with the one retained message of the list, and answers its
# ping, which tells the pod that no more retained messages follow.
port=18431
list=smoke-walk-1
topic="liken/library/libraries/smoke/movies/missing/appearances/$list/0"
python3 - "$port" "$topic" "$video" "$size" <<'PY' &
import json, socket, struct, sys
port, topic, path, size = int(sys.argv[1]), sys.argv[2], sys.argv[3], int(sys.argv[4])
payload = json.dumps({"path": path, "size": size, "durationMs": 4000}).encode()

def length(n):
    out = bytearray()
    while True:
        digit, n = n % 128, n // 128
        out.append(digit | (0x80 if n else 0))
        if not n:
            return bytes(out)

def packet(first, body):
    return bytes([first]) + length(len(body)) + body

def read(conn):
    first = conn.recv(1)
    if not first:
        return None, None
    n, shift = 0, 0
    while True:
        digit = conn.recv(1)[0]
        n += (digit & 0x7F) << shift
        shift += 7
        if not digit & 0x80:
            break
    body = b""
    while len(body) < n:
        body += conn.recv(n - len(body))
    return first[0], body

server = socket.socket()
server.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
server.bind(("127.0.0.1", port))
server.listen()
while True:
    conn, _ = server.accept()
    with conn:
        while True:
            first, body = read(conn)
            if first is None:
                break
            kind = first & 0xF0
            if kind == 0x10:
                conn.sendall(packet(0x20, b"\x00\x00"))
            elif kind == 0x80:
                conn.sendall(packet(0x90, body[:2] + b"\x00"))
                filter_length = struct.unpack(">H", body[2:4])[0]
                if body[4:4 + filter_length].decode() == topic:
                    name = topic.encode()
                    conn.sendall(packet(0x31, struct.pack(">H", len(name)) + name + payload))
            elif kind == 0xC0:
                conn.sendall(packet(0xD0, b""))
            elif kind == 0xE0:
                break
PY
broker=$!
trap 'kill "$broker" 2>/dev/null; rm -rf "$work"' EXIT
for _ in $(seq 50); do
  (exec 3<>"/dev/tcp/127.0.0.1/$port") 2>/dev/null && break
  sleep 0.1
done

run --network host --entrypoint /library-operator-pod \
  --env LIBRARY_FACT=appearances --env LIBRARY_KIND=movies --env LIBRARY_ROOT= \
  --env LIBRARY_NAMESPACE=smoke --env LIBRARY_NAME=movies \
  --env LIBRARY_BUS_ADDRESS="127.0.0.1:$port" --env LIBRARY_WORK_LIST="$list" \
  --env JOB_COMPLETION_INDEX=0 --env JOB_NAME=smoke "$image" worker
ledger="$work/$title/.liken/appearances.yaml"
grep -q 'result: found' "$ledger"
grep -q 'headshot: missing' "$ledger"
