#!/bin/sh
# Points apt at the snapshots of one date: Ubuntu 26.04 at
# snapshot.ubuntu.com and the INDI PPA at
# snapshot.ppa.launchpadcontent.net, each as it stood at midnight UTC.
# The PPA itself keeps only its newest build of each package, so only a
# snapshot installs the same files again.
#
# Both services answer only over HTTPS, and the Ubuntu image has no CA
# bundle. The Dockerfile fetches Mozilla's bundle from curl.se by its
# checksum to /etc/apt/cacert.pem before this runs, and this copies it
# to the path where apt and libcurl read a bundle.
#
# Usage: snapshot.sh <YYYYMMDD>
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

mkdir -p /etc/ssl/certs
cp /etc/apt/cacert.pem /etc/ssl/certs/ca-certificates.crt
echo 'Acquire::https::CAInfo "/etc/ssl/certs/ca-certificates.crt";' >/etc/apt/apt.conf.d/99snapshot-ca
rm -f /etc/apt/sources.list /etc/apt/sources.list.d/*
cat >/etc/apt/sources.list.d/snapshot.sources <<SOURCES
Types: deb
URIs: https://snapshot.ubuntu.com/ubuntu/$stamp/
Suites: resolute resolute-updates resolute-security
Components: main universe
Signed-By: /usr/share/keyrings/ubuntu-archive-keyring.gpg

Types: deb
URIs: https://snapshot.ppa.launchpadcontent.net/mutlaqja/ppa/ubuntu/$stamp/
Suites: resolute
Components: main
Signed-By: /etc/apt/trusted.gpg.d/mutlaqja.asc
SOURCES
