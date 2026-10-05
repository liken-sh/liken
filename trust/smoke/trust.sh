#!/usr/bin/env bash
# Checks the trust image before it ships: the bundle in it is the curl.se
# snapshot that VERSION names, by the checksum that curl.se publishes,
# and the three places that state the date agree.
#
# Usage: trust.sh <image>
set -euo pipefail

image=$1
here=$(cd "$(dirname "$0")/.." && pwd)
date=$(cat "$here/VERSION")

compact=${date//-/}
grep -qx "version = \"$compact\"" "$here/package.toml" || {
	echo "package.toml's version is not $compact, the date in VERSION" >&2
	exit 1
}
grep -q "cacert-$date.pem" "$here/Dockerfile" || {
	echo "the Dockerfile does not fetch cacert-$date.pem, the snapshot in VERSION" >&2
	exit 1
}

published=$(curl -fsS --retry 3 "https://curl.se/ca/cacert-$date.pem.sha256" | cut -d' ' -f1)
id=$(docker create "$image" /none)
trap 'docker rm "$id" >/dev/null' EXIT
held=$(docker cp "$id:/etc/ssl/certs/ca-certificates.crt" - | tar -xO | sha256sum | cut -d' ' -f1)
if [ "$held" != "$published" ]; then
	echo "the image holds a bundle with sha256 $held; curl.se publishes $published for $date" >&2
	exit 1
fi
grep -q "$published" "$here/Dockerfile" || {
	echo "the Dockerfile's checksum is not $published" >&2
	exit 1
}
echo "the trust image holds the Mozilla CA bundle of $date"
