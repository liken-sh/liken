#!/bin/sh
# Writes the license files of the Rust standard library and of every
# crate that a Rust program links into one directory tree, under
# /usr/share/doc/<crate>-<version>/, so that an image ships them beside
# the program.
#
# The MIT and Apache licenses of the standard library and of most
# crates permit the redistribution of a binary on one condition: the
# copyright notice and the license text go with it. A Rust binary
# holds neither, so an image that ships only the binary does not meet
# that condition.
#
# Usage: cargo.sh <out> [<cargo tree option>...]
#
# Run it in the build stage after the build, in the same RUN that
# mounts the crate registry, with the options that select what the
# build compiled:
#
#   sh /cargo.sh /notices -p idle-screen --no-default-features
#
# The final stage then copies the tree: COPY --from=build /notices /.
#
# A crate that this repository holds is liken's own code, under
# liken's MIT license, so the tree leaves it out. Some crates publish
# no license file and state their license only in Cargo.toml. For
# those, the tree holds the crate's Cargo.toml, which names the
# license and the authors.
set -eu

out=$1
shift

registry=${CARGO_HOME:-$HOME/.cargo}/registry/src

# The standard library is in every Rust binary. The toolchain carries
# its notices in COPYRIGHT-library.html, and the license texts that
# the notices name in licenses/. COPYRIGHT.html, beside them, covers
# the compiler, which no image ships.
sysroot=$(rustc --print sysroot)
if [ ! -f "$sysroot/share/doc/rust/COPYRIGHT-library.html" ]; then
	echo "cargo.sh: the toolchain at $sysroot holds no COPYRIGHT-library.html" >&2
	exit 1
fi
mkdir -p "$out/usr/share/doc/rust"
cp "$sysroot/share/doc/rust/COPYRIGHT-library.html" "$out/usr/share/doc/rust/"
cp -R "$sysroot/share/doc/rust/licenses" "$out/usr/share/doc/rust/"

# -e normal follows the edges that reach the binary, and leaves out the
# build and test dependencies. A crate of this repository prints its
# directory in parentheses, and a crate that cargo printed once already
# prints (*).
crates=$(cargo tree --locked -e normal --prefix none --format '{p}' "$@")

printf '%s\n' "$crates" | grep -v ' (/' | awk 'NF { print $1, $2 }' | sort -u | while read -r name version; do
	dir=$(find "$registry" -mindepth 1 -maxdepth 2 -type d -name "$name-${version#v}" | head -n 1)
	if [ -z "$dir" ]; then
		echo "cargo.sh: the registry holds no source of $name $version" >&2
		exit 1
	fi
	doc=$out/usr/share/doc/$name-${version#v}
	mkdir -p "$doc"
	find "$dir" -maxdepth 1 -type f \( -iname 'licen[cs]e*' -o -iname 'copying*' -o -iname 'notice*' -o -iname 'copyright*' \) \
		-exec cp {} "$doc/" \;
	if [ -z "$(ls "$doc")" ]; then
		cp "$dir/Cargo.toml" "$doc/"
	fi
done
