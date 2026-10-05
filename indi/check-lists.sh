#!/bin/sh
# Fails when a program that indi-3rdparty-drivers installs is in no list
# under images/, or in more than one. A new INDI release can add a
# driver, and this check makes each one a decision: a list of an image,
# or the list named unpublished.
#
# Usage: check-lists.sh <images directory>
set -eu

images=$1
listed=$(cat "$images"/* | awk '$1 == "driver" {print $2}' | sort)
installed=$(dpkg -L indi-3rdparty-drivers | grep '^/usr/bin/indi_' | xargs -n1 basename | sort)

twice=$(echo "$listed" | uniq -d)
if [ -n "$twice" ]; then
	echo "drivers in more than one list under images/:" >&2
	echo "$twice" >&2
	exit 1
fi
missing=$(echo "$installed" | while read -r d; do echo "$listed" | grep -qx "$d" || echo "$d"; done)
if [ -n "$missing" ]; then
	echo "drivers in no list under images/; add each to an image's list or to images/unpublished:" >&2
	echo "$missing" >&2
	exit 1
fi
gone=$(echo "$listed" | while read -r d; do echo "$installed" | grep -qx "$d" || echo "$d"; done)
if [ -n "$gone" ]; then
	echo "drivers listed under images/ that indi-3rdparty-drivers no longer installs:" >&2
	echo "$gone" >&2
	exit 1
fi
