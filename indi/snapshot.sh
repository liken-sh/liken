#!/bin/sh
# Points apt at the snapshots of one date: Ubuntu 26.04 at
# snapshot.ubuntu.com, and the INDI PPA and the PHD2 PPA at
# snapshot.ppa.launchpadcontent.net, each as it stood at midnight UTC,
# and reads their package lists.
# A PPA itself keeps only its newest build of each package, so only a
# snapshot installs the same files again. Only the indi-phd2 image
# installs from the PHD2 PPA, and no package of it replaces a package
# of the other two archives.
#
# Both services answer only over HTTPS, and the Ubuntu image has no CA
# bundle. The Dockerfile copies the trust component's bundle in before
# this runs, and apt reads it from the path below.
#
# apt checks each InRelease file against the keys in Signed-By, but a
# signature has no date that apt compares, so any InRelease file that
# the same keys once signed would pass. The script checks each one
# against its sha256 in snapshot.sha256 beside this script, which
# latest.sh --bump writes after it verifies the signatures. That file
# is in the image's recipe, so the date, the package lists, and the
# packages are bound together.
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

Types: deb
URIs: https://snapshot.ppa.launchpadcontent.net/pch/phd2/ubuntu/$stamp/
Suites: resolute
Components: main
Signed-By: /etc/apt/trusted.gpg.d/pch.asc
SOURCES

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
