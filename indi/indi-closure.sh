#!/bin/sh
# Builds the tree of one INDI image from its list in images/, less every
# file of the trees named after it.
#
# Usage: indi-closure.sh <out> <list> [<base tree>...]
#
# Each line of a list is one entry:
#
#   driver <name>    the driver /usr/bin/<name>
#   package <name>   every /usr/bin/indi_* program that the package installs
#   seed <path>      a program or a library, or every file in a directory
#   sdk <name>       every file of indi-3rdparty-libs whose name starts
#                    with <name>.so: a vendor SDK, whole, because a closed
#                    SDK can open files after it finds a camera, and no
#                    test without the camera reaches that code
#   data <path>      a file or a directory, copied as it is
#   link <path> <target>
#                    a symbolic link
#
# Run it in the builder, which has every package installed.
set -eu

# shellcheck source=closure.sh
. "$(dirname "$0")/closure.sh"

out=$1
list=$2
shift 2

seeds=""
data=""
links=""
while read -r kind name target; do
	case $kind in
	'' | '#'*) continue ;;
	driver) seeds="$seeds /usr/bin/$name" ;;
	package) seeds="$seeds $(dpkg -L "$name" | grep '^/usr/bin/indi_' | tr '\n' ' ')" ;;
	seed)
		if [ -d "$name" ]; then
			seeds="$seeds $(find "$name" -type f | sort | tr '\n' ' ')"
		else
			seeds="$seeds $name"
		fi
		;;
	sdk)
		files=$(dpkg -L indi-3rdparty-libs | grep "/$name\.so" || true)
		if [ -z "$files" ]; then
			echo "$list: indi-3rdparty-libs installs no $name.so" >&2
			exit 1
		fi
		seeds="$seeds $(echo "$files" | tr '\n' ' ')"
		;;
	data) data="$data $name" ;;
	link) links="$links $name=$target" ;;
	*)
		echo "$list: an entry of an unknown kind: $kind $name" >&2
		exit 1
		;;
	esac
done <"$list"

for seed in $seeds; do
	if [ ! -e "$seed" ]; then
		echo "$list: $seed is not in the builder" >&2
		exit 1
	fi
done

collect "$out" "$seeds" "$data"
for entry in $links; do
	mkdir -p "$out$(dirname "${entry%%=*}")"
	ln -s "${entry#*=}" "$out${entry%%=*}"
done

for base in "$@"; do
	subtract "$out" "$base"
done
