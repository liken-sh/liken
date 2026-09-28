#!/usr/bin/env bash
# Start the redirect image and ask it for one subdomain's page, to prove
# that the binary starts, listens, and answers with the manual's address.
#
#   redirect/smoke/redirect.sh <image reference>
set -euo pipefail

image=${1:?usage: redirect.sh <image reference>}
name="redirect-smoke-$$"
trap 'docker rm -f "$name" >/dev/null 2>&1 || true' EXIT
docker run -d --name "$name" -p 127.0.0.1::8080 "$image" >/dev/null
port=$(docker port "$name" 8080/tcp | head -n 1 | sed 's/.*://')

for _ in $(seq 1 20); do
  if curl --silent --output /dev/null "http://127.0.0.1:$port/healthz"; then
    break
  fi
  sleep 0.5
done
location=$(curl --silent --output /dev/null --write-out '%{redirect_url}' \
  --header 'Host: display.liken.sh' "http://127.0.0.1:$port/docs/guides/install/")
if [ "$location" != "https://liken.sh/display/docs/guides/install/" ]; then
  echo "display.liken.sh redirected to '$location'" >&2
  exit 1
fi
