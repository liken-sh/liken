#!/bin/sh
# Points apt at one day of snapshot.debian.org and reads its package
# lists, so that every build of a base installs the same package
# versions. The date is the base's version in its package.toml, which
# the bake file passes to the build as VERSION. Every base on Debian
# runs this file from the vulkan directory, the lowest base, so that
# one script states where the packages come from.
#
# Usage: snapshot.sh <YYYYMMDD>
#
# The archive and the security archive both come from the snapshot at
# midnight UTC of that day. The Release files of an old snapshot are
# past their Valid-Until date, so apt must not check it. apt still
# checks every Release file against the Debian archive keys, and every
# package against the Release file.
#
# The sources name http, not https, because the slim image holds no CA
# certificates. Without a date check and without TLS, a host on the
# path could serve an older InRelease file that Debian also signed, and
# apt would accept it. So the script checks each InRelease file against
# its sha256 in snapshot.sha256 beside this script. That file is in the
# vulkan image's recipe, so the date, the package lists, and the
# packages are bound together.
set -eu

date=${1:?usage: snapshot.sh <YYYYMMDD>}
case $date in
[0-9][0-9][0-9][0-9][0-9][0-9][0-9][0-9]) ;;
*)
	echo "snapshot.sh: $date is not a date in the form YYYYMMDD" >&2
	exit 1
	;;
esac
stamp=${date}T000000Z

rm -f /etc/apt/sources.list /etc/apt/sources.list.d/*
cat >/etc/apt/sources.list.d/snapshot.sources <<EOF
Types: deb
URIs: http://snapshot.debian.org/archive/debian/$stamp/
Suites: trixie trixie-updates
Components: main
Signed-By: /usr/share/keyrings/debian-archive-keyring.pgp
Check-Valid-Until: no

Types: deb
URIs: http://snapshot.debian.org/archive/debian-security/$stamp/
Suites: trixie-security
Components: main
Signed-By: /usr/share/keyrings/debian-archive-keyring.pgp
Check-Valid-Until: no
EOF

# snapshot.debian.org limits how fast one client may fetch, and it
# answers a burst with an error. A retry after a pause succeeds.
cat >/etc/apt/apt.conf.d/80-snapshot <<EOF
Acquire::Retries "5";
Acquire::http::Timeout "60";
EOF

apt-get update

# Every InRelease file that apt read needs a line in snapshot.sha256,
# and every line of this date needs its file, with the same sha256. A
# date with no lines there is a date whose lists nobody checked.
sums=$(dirname "$0")/snapshot.sha256
for list in /var/lib/apt/lists/*_InRelease; do
	if ! grep -q "  ${list##*/}\$" "$sums"; then
		echo "snapshot.sh: $sums has no checksum for ${list##*/}" >&2
		exit 1
	fi
done
if ! grep -q "_${stamp}_" "$sums"; then
	echo "snapshot.sh: $sums has no checksums for $stamp" >&2
	exit 1
fi
grep "_${stamp}_" "$sums" | (cd /var/lib/apt/lists && sha256sum -c --strict -)
