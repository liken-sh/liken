#!/usr/bin/env bash
# Proves the library-operator-titles image before it ships.
#
# Usage: library-operator-titles.sh <image>
#
#   <image>  the full reference of the titles image, for example
#            ghcr.io/liken-sh/library-operator-titles:2026.11.14-001.
#            The image must be in the local Docker daemon.
#
# The tool opens OpenVINO, its CPU plugin, and the models when `read`
# runs, not when it starts, so only a read proves them. The image's own
# ffmpeg makes a short test video in a movie folder, with the time of
# each frame drawn on it, and the worker runs on it the way a pod of the
# worker Job runs: the pod program reads its one video from the bus,
# runs `titles read`, and writes the ledger. A worker pod reads the
# retained message at its index from the broker, and this script stands
# in for the broker with a server that holds that one message, because
# the broker is upstream's, and the session the pod speaks is proved by
# the Go tests and on a test cluster.
set -euo pipefail

image=$1

# The image must hold the notices of the third-party software in it,
# as notices/check.sh describes, the notice and the licenses of the
# models, and the SBOM record of OpenVINO and the models, in which syft
# finds no package of its own.
"$(dirname "$0")/../../notices/check.sh" "$image" \
  usr/share/doc/go/LICENSE \
  usr/share/doc/rust/COPYRIGHT-library.html \
  usr/share/doc/openvino/EULA.txt \
  usr/share/titles/models/NOTICE \
  usr/share/titles/models/text_detection_en_ppocrv3_2023may.LICENSE \
  usr/share/titles/models/text_recognition_ch_ppocrv4.LICENSE \
  usr/share/doc/liken/library-operator-titles.cdx.json

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

# The containers run as this user, so the files they write in the work
# directory are this user's to delete.
run() {
  docker run --rm --user "$(id -u):$(id -g)" --volume "$work:/library" "$@"
}

title="Test Pattern (2026)"
video="$title/$title.mkv"
mkdir -p "$work/$title"

# Eight seconds of ffmpeg's test pattern, which draws the time of each
# frame in a font of its own, so the detector has a box to find and the
# reader a line to read. The image holds no font file, so a filter that
# draws text from one cannot run in it. The pattern is made small and
# scaled up, which makes its text as tall as a line of end titles.
run --entrypoint /usr/bin/ffmpeg "$image" -hide_banner -loglevel error \
  -f lavfi -i "testsrc2=duration=8:size=320x180:rate=24,scale=1280:720:flags=neighbor" \
  -c:v mpeg4 -q:v 3 -g 24 "/library/$video"

# The tool alone, as a person runs it from the image. The reading holds
# a header and a line for each second, and the reader read the time
# that the pattern draws.
run "$image" read "/library/$video"
reading="$work/$title/.liken/titles/$title.mkv.jsonl"
test "$(wc -l < "$reading")" -ge 8
grep -q '"00:00:0' "$reading"

# The worker. The reading goes first, so the worker runs the read itself.
rm "$reading"
size=$(stat -c %s "$work/$video")

# The stand-in broker: it accepts the pod's connection, answers its
# subscription with the one retained message of the list, and answers its
# ping, which tells the pod that no more retained messages follow.
port=18432
list=smoke-walk-1
topic="liken/library/libraries/smoke/movies/missing/titles/$list/0"
python3 - "$port" "$topic" "$video" "$size" <<'PY' &
import json, socket, struct, sys
port, topic, path, size = int(sys.argv[1]), sys.argv[2], sys.argv[3], int(sys.argv[4])
payload = json.dumps({"path": path, "size": size, "durationMs": 8000}).encode()

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
  --env LIBRARY_FACT=titles --env LIBRARY_KIND=movies --env LIBRARY_ROOT= \
  --env LIBRARY_NAMESPACE=smoke --env LIBRARY_NAME=movies \
  --env LIBRARY_BUS_ADDRESS="127.0.0.1:$port" --env LIBRARY_WORK_LIST="$list" \
  --env JOB_COMPLETION_INDEX=0 --env JOB_NAME=smoke "$image" worker
grep -q 'result: found' "$work/$title/.liken/titles.yaml"
test -s "$reading"
