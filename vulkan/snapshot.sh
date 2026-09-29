#!/bin/sh
# Points apt at one day of snapshot.debian.org, so that every build of
# a base installs the same package versions. The date is the base's
# version in its package.toml, which the bake file passes to the build
# as VERSION. Every base on Debian sources this file from the vulkan
# directory, the lowest base, so that one script states where the
# packages come from.
#
# Usage: snapshot.sh <YYYYMMDD>
#
# The archive and the security archive both come from the snapshot at
# midnight UTC of that day. The Release files of an old snapshot are
# past their Valid-Until date, so apt must not check it. apt still
# checks every Release file against the Debian archive keys, and every
# package against the Release file, so the packages are the ones
# Debian signed.
#
# The sources name http, not https. The slim image holds no CA
# certificates, so apt cannot verify a TLS peer before it installs
# them, and the signatures above are what prove the packages.
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
