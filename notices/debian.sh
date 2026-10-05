#!/bin/sh
# Writes into a closure's tree the notices that the licenses of its
# files ask to travel with them, for an image that ships files of
# Debian packages without the packages themselves.
#
# Each Debian package states its copyright and its license in
# /usr/share/doc/<package>/copyright, and a closure copies only what
# the loader resolves, which leaves those files behind. So for each
# package that a file of the tree came from, this copies the package's
# copyright file, and it copies the license texts in
# /usr/share/common-licenses that the copyright files name by path.
#
# The GPL and the LGPL also require that the source be available. The
# list /usr/share/doc/liken/<image>.packages names each package with
# its version and its source package, and snapshot.debian.org serves
# the source of every version that Debian published.
#
# Usage: debian.sh <out> <image>
#
# Run it in the closure's builder, after the closure, with the name of
# the image on ghcr.io/liken-sh:
#
#   sh /debian.sh /out audio-operator
#
# The pinned bases and the INDI images run the same steps from their
# own closure.sh, so that this file is not in their recipes.
#
# Debian policy requires a copyright file in every package, so a
# package without one fails the build here.
set -eu

out=$1
image=$2
packages=$out/usr/share/doc/liken/$image.packages

# holds succeeds when the tree holds a file of the package $1. A
# directory does not count, because many packages own the same
# directories, such as /usr/lib.
holds() {
	dpkg -L "$1" | while read -r path; do
		if [ ! -d "$out$path" ] && { [ -e "$out$path" ] || [ -L "$out$path" ]; }; then
			echo "$path"
			break
		fi
	done | grep -q .
}

mkdir -p "$out/usr/share/doc/liken"
cat >"$packages" <<EOF
ghcr.io/liken-sh/$image holds files of the Debian packages below,
unmodified. /usr/share/doc/<package>/copyright states the copyright
and the license of each package, and /usr/share/common-licenses holds
the license texts that those files name. snapshot.debian.org serves
the source of each package at
https://snapshot.debian.org/package/<source>/<source version>/.

<package> <version> <source> <source version>
EOF
for package in $(dpkg-query -W -f '${Package}\n'); do
	if ! holds "$package"; then
		continue
	fi
	mkdir -p "$out/usr/share/doc/$package"
	cp -L "/usr/share/doc/$package/copyright" "$out/usr/share/doc/$package/copyright"
	dpkg-query -W -f '${Package} ${Version} ${source:Package} ${source:Version}\n' "$package" >>"$packages"
done
cp -a --parents /usr/share/common-licenses "$out"
