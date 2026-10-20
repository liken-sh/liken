#!/usr/bin/env bash
# Checks the trust image before it ships: the bundle in it is the curl.se
# snapshot that VERSION names, by the checksum that curl.se publishes,
# and the three places that state the date agree.
#
# Usage: trust.sh <image>
set -euo pipefail

image=$1

# The image must hold the notices of the third-party software in it,
# as notices/check.sh describes.
"$(dirname "$0")/../../notices/check.sh" "$image" \
  usr/share/doc/mozilla-ca/copyright
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
work=$(mktemp -d)
id=$(docker create "$image" /none)
trap 'docker rm "$id" >/dev/null; rm -rf "$work"' EXIT
bundle=$work/ca-certificates.crt
docker cp "$id:/etc/ssl/certs/ca-certificates.crt" - | tar -xO >"$bundle"
held=$(sha256sum "$bundle" | cut -d' ' -f1)
if [ "$held" != "$published" ]; then
	echo "the image holds a bundle with sha256 $held; curl.se publishes $published for $date" >&2
	exit 1
fi
grep -q "$published" "$here/Dockerfile" || {
	echo "the Dockerfile's checksum is not $published" >&2
	exit 1
}

# The private keys of eight eMudhra intermediate CAs were compromised
# in October 2026 (https://emudhra.we-have-a-problem.com/), so a
# machine that trusts a root above them accepts a forged certificate
# for any host. These are the roots that sign those intermediates, by
# their SHA-256 fingerprints. A snapshot that holds one of them must
# not ship, even when its checksum matches.
distrusted="86A1ECBA089C4A8D3BBE2734C612BA341D813E043CF9E8A862CD5C57A36BBE6B emSign ECC Root CA - G3
D7A7A0FB5D7E2731D771E9484EBCDEF71D5F0C3E0A2948782BC83EE0EA699EF4 AAA Certificate Services
7DD78D5F4F13459A83DFF9ABBBA62EDBAF6F2D102BF257FD712F4D9F2746ED8D emSign Root TLS CA - G3
F2EDAAFF3D9B701FB4D14C9F285F8C0BAD90FC3BCC1FA07D47F4718EDABA34A5 emSign Root CS CA - G3"

# awk hands each certificate in the bundle to openssl on its own, and
# the fingerprints come back without colons, in upper case.
awk -v work="$work" '/-----BEGIN CERTIFICATE-----/ { n++ } n { print >(work "/cert-" n ".pem") }' "$bundle"
for cert in "$work"/cert-*.pem; do
	openssl x509 -in "$cert" -noout -fingerprint -sha256 | cut -d= -f2 | tr -d : | tr a-f A-F
done >"$work/fingerprints"

found=0
while read -r fingerprint name; do
	if grep -qx "$fingerprint" "$work/fingerprints"; then
		echo "the bundle holds $name ($fingerprint), a root of the compromised eMudhra intermediates" >&2
		found=1
	fi
done <<<"$distrusted"
[ "$found" -eq 0 ] || exit 1

echo "the trust image holds the Mozilla CA bundle of $date"
