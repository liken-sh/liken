#!/bin/sh
# The functions that collect a program, everything it loads, and its
# data files into one directory tree, so that an image ships that tree
# and nothing else, with the notices of the packages it came from.
# indi-closure.sh sources this file.
#
# This is the same method as vulkan/closure.sh, in a copy of its own.
# Reading that file through a build context would put every file of
# vulkan/ into this component's recipe, and each bump of the Debian
# bases would then force a new revision of every INDI image, which
# CI builds only when INDI itself is bumped.
set -eu

# The multiarch directory that holds every library below. dpkg names
# it for the architecture this builds on, so no architecture is written
# down here.
lib=$(dirname "$(dpkg -L libc6 | grep '/libc\.so\.6$')")

# Every hop of a symlink chain, then the file at the end of it. A
# soname is a link to a versioned file, and the loader opens the
# soname, so copying only one end of the chain breaks the load.
hops() {
	path=$1
	while [ -L "$path" ]; do
		printf '%s\n' "$path"
		target=$(readlink "$path")
		case $target in
		/*) path=$target ;;
		*) path=$(dirname "$path")/$target ;;
		esac
	done
	printf '%s\n' "$path"
}

# ldd prints the whole DT_NEEDED graph of one file, so one call for
# each seed reaches every library the loader resolves at load time.
# linux-vdso has no file behind it, and the loader's own line prints
# with no arrow. A library the loader cannot find prints "not found",
# and a tree missing one library fails at the first start in a pod, so
# the build fails here instead.
needed() {
	if ldd "$1" | grep -q 'not found'; then
		echo "$1 needs a library this builder does not have:" >&2
		ldd "$1" | grep 'not found' >&2
		exit 1
	fi
	ldd "$1" | sed -n 's/.*=> \(\/[^ ]*\).*/\1/p; s/^\t\(\/[^ ]*\) (0x.*/\1/p'
}

# collect writes the closure of $seeds and the files in $data to $out,
# then builds the loader's cache there.
#
# /lib and /lib64 are symlinks to the directories under /usr, and ldd
# reports every library under the name it resolved, which is the one
# that goes through them. The loader's own path names /lib64. Copying
# the two links first lets every copy below write the path exactly as
# it was resolved.
#
# Without a cache the loader searches its built-in directory list on
# every open, and that list does not name the multiarch directory that
# holds every library above.
collect() {
	out=$1
	seeds=$2
	data=$3

	mkdir -p "$out$lib" "$out/usr/lib64" "$out/usr/bin"
	for link in /lib /lib64; do
		if [ -L "$link" ]; then
			cp -a --parents "$link" "$out"
		fi
	done

	for seed in $seeds; do
		{
			hops "$seed"
			needed "$(readlink -f "$seed")" | while read -r path; do hops "$path"; done
		} >>"$out/.closure"
	done
	sort -u "$out/.closure" | while read -r path; do
		cp -a --parents "$path" "$out"
	done
	rm -f "$out/.closure"

	for path in $data; do
		cp -a --parents "$path" "$out"
	done

	mkdir -p "$out/etc"
	printf '%s\n' "$lib" >"$out/etc/ld.so.conf"
	ldconfig -r "$out"
}

# notices writes into $out the notices that the licenses of its files
# ask to travel with them. Each Ubuntu and PPA package states its
# copyright and its license in /usr/share/doc/<package>/copyright, and
# a closure copies only what the loader resolves, which leaves those
# files behind. So for each package that a file of $out came from,
# notices copies the package's copyright file, and it copies the
# license texts in /usr/share/common-licenses that the copyright files
# name by path.
#
# The GPL and the LGPL also require that the source be available. The
# list /usr/share/doc/liken/<image>.packages names each package with its
# version and its source package, and the snapshots that the builder
# installs from serve the sources of the same date. The list states
# those snapshots as deb-src entries.
#
# Debian policy requires a copyright file in every package, so a
# package without one fails the build here.
notices() {
	out=$1
	image=$2
	packages=$out/usr/share/doc/liken/$image.packages

	mkdir -p "$out/usr/share/doc/liken"
	cat >"$packages" <<EOF
ghcr.io/liken-sh/$image holds files of the Ubuntu and INDI PPA
packages below, unmodified. /usr/share/doc/<package>/copyright states
the copyright and the license of each package, and
/usr/share/common-licenses holds the license texts that those files
name. The copyright file of indi-3rdparty-libs does not name the
vendor SDKs in that package. So when this image holds an SDK whose
directory in the indi-3rdparty repository holds a license file,
/usr/share/doc/indi-3rdparty-libs/<directory>/ holds that file.

The snapshots that the image installs from serve the source of each
package. With these sources, apt-get source <source>=<source version>
fetches it:

$(sed 's/^Types: deb$/Types: deb-src/' /etc/apt/sources.list.d/snapshot.sources)

<package> <version> <source> <source version>
EOF
	for package in $(dpkg-query -W -f '${Package}\n'); do
		if ! holds "$out" "$package"; then
			continue
		fi
		mkdir -p "$out/usr/share/doc/$package"
		cp -L "/usr/share/doc/$package/copyright" "$out/usr/share/doc/$package/copyright"
		dpkg-query -W -f '${Package} ${Version} ${source:Package} ${source:Version}\n' "$package" >>"$packages"
	done
	cp -a --parents /usr/share/common-licenses "$out"
}

# holds succeeds when the tree $1 holds a file of the package $2. A
# directory does not count, because many packages own the same
# directories, such as /usr/lib.
holds() {
	dpkg -L "$2" | while read -r path; do
		if [ ! -d "$1$path" ] && { [ -e "$1$path" ] || [ -L "$1$path" ]; }; then
			echo "$path"
			break
		fi
	done | grep -q .
}

# subtract removes from $out every file that $base already holds with
# the same content, so that an image built FROM the base image adds a
# layer of only what the base lacks. Docker writes a copied file into
# the new layer whether or not a lower layer has it, so a tree that
# repeats the base would carry the base twice. The loader's cache
# stays, because each tree builds its own and the two differ.
subtract() {
	out=$1
	base=$2

	(cd "$base" && find . \( -type f -o -type l \) -print) | while read -r path; do
		case $path in
		./etc/ld.so.cache | ./etc/ld.so.conf) continue ;;
		esac
		mine=$out/$path
		theirs=$base/$path
		if [ -L "$mine" ] && [ -L "$theirs" ]; then
			if [ "$(readlink "$mine")" = "$(readlink "$theirs")" ]; then
				rm "$mine"
			fi
		elif [ -f "$mine" ] && [ -f "$theirs" ] && cmp -s "$mine" "$theirs"; then
			rm "$mine"
		fi
	done
	(cd "$out" && find . -depth -type d -empty -delete)
}
