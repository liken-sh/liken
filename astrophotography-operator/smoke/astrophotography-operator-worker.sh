#!/usr/bin/env bash
# Solves a synthetic star field through the worker image's two HTTP
# servers before the image ships.
#
# Usage: astrophotography-operator-worker.sh <image>
#
#   <image>  the full reference of the worker image, for example
#            ghcr.io/liken-sh/astrophotography-operator-worker:2026.10.12-001.
#            The image must be in the local Docker daemon.
#
# The check runs a solve the way the operator does in a cluster: the
# worker mode reads a frame from an outbox and answers its star list,
# and the solver mode solves that list against its index files. Each
# container runs as the caller on a read-only root, with the emptyDirs
# of its pod as tmpfs mounts, as the operator's Deployments run them.
# The frame and its index file are the astrometry image's synthetic
# field, whose center is 150°, 30° at 2.235 arcseconds per pixel.
set -euo pipefail

image=$1
here=$(cd "$(dirname "$0")" && pwd)
field="$here/../../astrometry/smoke"

# The image must hold the notices of the third-party software in it,
# as notices/check.sh describes: the astrometry image's and the Go
# binary's.
"$here/../../notices/check.sh" "$image" usr/share/doc/go/LICENSE

work=$(mktemp -d)
containers=()
cleanup() {
  if [ ${#containers[@]} -gt 0 ]; then
    docker rm -f "${containers[@]}" > /dev/null
  fi
  rm -rf "$work"
}
trap cleanup EXIT

frame=cam/tonight/_centering/smoke.fits
mkdir -p "$work/outbox/$(dirname "$frame")" "$work/indexes"
cp "$field/field.fits" "$work/outbox/$frame"
cp "$field/index.fits" "$work/indexes/index-synthetic.fits"
chmod -R a+rwX "$work"

# start runs one mode and records its container, so the cleanup
# removes it. It runs in this shell, not in a command substitution,
# because a subshell's record would be lost.
start() {
  containers+=("$(docker run -d --read-only --user "$(id -u):$(id -g)" \
    --tmpfs /work:mode=1777 --tmpfs /tmp:mode=1777 -p 127.0.0.1::8080 "$@")")
}
# address answers the loopback address of a container's port 8080.
address() {
  docker port "$1" 8080/tcp | head -n 1
}
start -v "$work/indexes:/indexes:ro" "$image" solver
start -v "$work/outbox:/outbox" "$image" worker
solver=$(address "${containers[0]}")
worker=$(address "${containers[1]}")

# Each server listens only when it is ready. The solver reads the
# headers of its index files first. Thirty tries a second apart bound
# the wait.
for address in "$solver/indexes" "$worker/outbox"; do
  for try in $(seq 30); do
    if curl -sf "http://$address" > /dev/null; then
      break
    fi
    if [ "$try" = 30 ]; then
      echo "http://$address did not answer in 30 s" >&2
      docker logs "${containers[0]}" >&2
      exit 1
    fi
    sleep 1
  done
done

curl -sf "http://$solver/indexes" | tee "$work/indexes.json"
echo
jq -e 'length == 1 and .[0].id == 1 and .[0].files == 1' "$work/indexes.json" > /dev/null

hint='{"center":{"ra":150,"dec":30},"radius":5,"scaleLow":2.0,"scaleHigh":2.5}'
curl -sf -X POST -H 'Content-Type: application/json' \
  -d "{\"frame\":\"$frame\",\"hint\":$hint}" "http://$worker/extract" > "$work/extract.json"
jq -r '"extracted \(.stars) stars of median FWHM \(.medianFWHM) pixels"' "$work/extract.json"
jq -r .axy "$work/extract.json" | base64 -d > "$work/field.axy"

curl -sf -X POST -H 'Content-Type: application/octet-stream' \
  --data-binary "@$work/field.axy" "http://$solver/solve" | tee "$work/solve.json"
echo
# The center must be the field's within one arcsecond, and the scale
# within 1%, as the astrometry image's own smoke check requires.
jq -e '.solved and ((.center.ra - 150) | fabs) < 1/3600 and ((.center.dec - 30) | fabs) < 1/3600
  and ((.scale - 2.235) | fabs) < 0.02235' "$work/solve.json" > /dev/null

# The worker deletes the centering frame after its solve.
curl -sf -X DELETE "http://$worker/centering/smoke"
if [ -e "$work/outbox/$frame" ]; then
  echo "the worker did not delete $frame" >&2
  exit 1
fi
echo "solved the synthetic field through the worker and the solver"
