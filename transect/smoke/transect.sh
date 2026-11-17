#!/usr/bin/env bash
# Proves the transect image before it ships.
#
# Usage: transect.sh <image>
#
#   <image>  the full reference of the transect image, for example
#            ghcr.io/liken-sh/transect:2026.11.17-001. The image must
#            be in the local Docker daemon.
#
# The runner has no cluster, so the server does not start here. What
# can break without a cluster is the closure and the notices, so the
# check reads the image's files, and the binary proves that the loader
# resolves its libraries by answering one flag.
set -euo pipefail

image=$1

# The image must hold the notices of the third-party software in it,
# as notices/check.sh describes, and the SBOM record of the htmx files
# that the binary embeds.
"$(dirname "$0")/../../notices/check.sh" "$image" \
  usr/share/doc/rust/COPYRIGHT-library.html \
  usr/share/doc/source-sans-3/LICENSE.md \
  usr/share/doc/htmx.org/LICENSE \
  usr/share/doc/htmx-ext-sse/LICENSE \
  usr/share/doc/mozilla-ca/copyright \
  usr/share/doc/liken/transect.cdx.json \
  etc/ssl/certs/ca-certificates.crt

# deploy/transect.yaml runs the pod as 65534, and the image names the
# same user, so `docker run` runs the binary the way a pod does.
user=$(docker image inspect --format '{{.Config.User}}' "$image")
[[ "$user" == 65534:65534 ]] \
  || { echo "the image runs as '$user', not 65534:65534"; exit 1; }

docker run --rm "$image" --help
