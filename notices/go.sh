#!/bin/sh
# Writes the license files of the Go standard library and of every
# module that a Go program links into one directory tree, under
# /usr/share/doc/<module>/, so that an image ships them beside the
# program.
#
# The BSD, MIT, and Apache licenses of the standard library and the
# modules each permit the redistribution of a binary on one condition:
# the copyright notice and the license text go with it. A static Go
# binary holds neither, so an image that ships only the binary does
# not meet that condition.
#
# Usage: go.sh <out> <package>...
#
# Run it in the build stage after the build, in the module's directory,
# with the build's environment, and name the packages that the build
# compiled:
#
#   sh /go.sh /notices . ./cmd/pod
#
# The final stage then copies the tree: COPY --from=build /notices /.
#
# The module itself and each module that go.mod replaces with a
# directory of this repository are liken's own code, under liken's MIT
# license, so the tree leaves them out. A module with no license file
# at its root fails the build, because the image cannot ship a notice
# that the module does not hold.
set -eu

out=$1
shift

# go list prints the module of every package that the named packages
# import, the standard library's packages excepted, which have no
# module. A replaced module with no version is a directory.
modules=$(go list -deps -f '{{with .Module}}{{if not .Main}}{{if or (not .Replace) .Replace.Version}}{{.Path}} {{.Dir}}{{end}}{{end}}{{end}}' "$@" | sort -u)

printf 'go %s\n%s\n' "$(go env GOROOT)" "$modules" | while read -r module dir; do
	if [ -z "$module" ]; then
		continue
	fi
	doc=$out/usr/share/doc/$module
	mkdir -p "$doc"
	find "$dir" -maxdepth 1 -type f \( -iname 'licen[cs]e*' -o -iname 'copying*' -o -iname 'notice*' -o -iname 'patents*' \) \
		-exec cp {} "$doc/" \;
	if [ -z "$(ls "$doc")" ]; then
		echo "go.sh: $module holds no license file in $dir" >&2
		exit 1
	fi
done
