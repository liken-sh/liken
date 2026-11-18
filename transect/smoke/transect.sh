#!/usr/bin/env bash
# Checks the transect image before it is published.
#
# Usage: transect.sh <image>
#
#   <image>  the full reference of the transect image, for example
#            ghcr.io/liken-sh/transect:2026.11.17-001. The image must
#            be in the local Docker daemon.
#
# The runner has no cluster, so the server cannot start here. The
# script checks that the image has its notices and its CA bundle, and
# runs the binary with --help, which fails when the dynamic loader
# cannot find one of the binary's libraries.
set -euo pipefail

image=$1

# The license notices of the third-party software, as notices/check.sh
# describes, the SBOM record of the htmx files that the binary embeds,
# and the CA bundle.
"$(dirname "$0")/../../notices/check.sh" "$image" \
  usr/share/doc/rust/COPYRIGHT-library.html \
  usr/share/doc/source-sans-3/LICENSE.md \
  usr/share/doc/htmx.org/LICENSE \
  usr/share/doc/htmx-ext-sse/LICENSE \
  usr/share/doc/mozilla-ca/copyright \
  usr/share/doc/liken/transect.cdx.json \
  etc/ssl/certs/ca-certificates.crt

# deploy/transect.yaml runs the pod as user 65534. The image sets the
# same user, so `docker run` runs the binary as the pod does.
user=$(docker image inspect --format '{{.Config.User}}' "$image")
[[ "$user" == 65534:65534 ]] \
  || { echo "the image runs as '$user', not 65534:65534"; exit 1; }

docker run --rm "$image" --help
