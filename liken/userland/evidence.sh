#!/usr/bin/env bash
#
# Write the evidence of which packages each shipped program contains,
# into <output>/evidence/. build.sh runs this script in the `buildroot`
# image after the build, and copies the directory into dist/.
# releases/sbom reads it and gives the scope excluded to each
# buildroot package that no file of a release contains. buildroot
# builds each package that a selected recipe depends on, and its SBOM
# lists every one, so the SBOM alone cannot tell a library that a
# program links from a library that only the build used.
#
# os.config makes GNU ld write a link map of each target link into
# <output>/maps/, named for the file that the link wrote. For each
# program, this script copies two parts of the map into
# evidence/maps/<program>.map:
#
#   - the section "Archive member included to satisfy reference by
#     file (symbol)", which names each member that the link took from
#     an archive, and on an indented line or after it, the file and
#     symbol that needed it. ld names a member as archive(member), or
#     by the member's own path when the archive is a thin archive, the
#     kind that meson writes. The section ends at its first empty line
#     after a member;
#   - each LOAD line, which names a file on the link's command line,
#     such as musl's crt1.o.
#
# The rest of the map gives the address of each section. It takes
# megabytes and names no package. A recipe that does not pass
# buildroot's LDFLAGS to its link leaves no map, and the script stops,
# so a program never ships without its evidence.
#
# The script also copies buildroot's packages-file-list.txt,
# packages-file-list-staging.txt, and packages-file-list-host.txt,
# which name the package that installed each file of the target tree,
# the staging tree, and the host tree.
#
# The linker names an absolute path the way the compiler found it,
# which can pass through a symlink such as lib64 or staging, and
# buildroot's lists name each file by its real path. buildroot's
# staging symlink is absolute, so only a process inside the container
# can follow it, and this script writes each absolute path as its real
# path from the root of the output, with a slash in front:
# /staging/usr/lib/libc.a, /host/lib/gcc/..., /target/..., or
# /build/<package>-<version>/.... A path with no slash in front is one
# that the linker took relative to the directory that it ran in, and
# the script leaves it as the map gives it. The script also leaves the
# file and symbol that needed a member as the map gives them, because
# they are for a person to read.
#
# Usage:
#   evidence.sh <output directory> <program>...

set -euo pipefail

(($# >= 2)) || {
    echo "usage: evidence.sh <output directory> <program>..." >&2
    exit 2
}
output="$1"
shift

stop() {
    echo "evidence.sh: $*" >&2
    exit 1
}

# canonical <path>: set real to the real path of an absolute path in
# the map, from the root of the output. The staging tree is inside the
# host tree, so it is tried first. The function sets a variable and
# prints nothing, so it runs in this shell and its cache lasts for the
# whole run.
declare -A roots=() canonical_paths=()
for tree in staging target host build; do
    roots[$tree]="$(realpath "$output/$tree")"
done
canonical() {
    local path="$1" resolved tree
    if [[ -z "${canonical_paths[$path]:-}" ]]; then
        resolved="$(realpath -e "$path")" || stop "a link map names $path, which does not exist"
        for tree in staging target host build; do
            if [[ "$resolved" == "${roots[$tree]}/"* ]]; then
                canonical_paths[$path]="/$tree/${resolved#"${roots[$tree]}/"}"
                break
            fi
        done
        [[ -n "${canonical_paths[$path]:-}" ]] ||
            stop "a link map names $path, which is outside buildroot's target, staging, host, and build trees"
    fi
    real="${canonical_paths[$path]}"
}

mkdir -p "$output/evidence/maps"
for program in "$@"; do
    map="$output/maps/$program.map"
    [[ -f "$map" ]] ||
        stop "buildroot wrote no link map of $program, so its recipe does not pass BR2_TARGET_LDFLAGS to the linker. Pass TARGET_LDFLAGS in the recipe's link"
    while IFS= read -r line; do
        case "$line" in
        "LOAD /"*)
            canonical "${line#LOAD }"
            echo "LOAD $real"
            ;;
        /*)
            first="${line%%[[:space:]]*}"
            file="${first%%(*}"
            canonical "$file"
            echo "$real${line#"$file"}"
            ;;
        *) echo "$line" ;;
        esac
    done < <(awk '/^Archive member included to satisfy reference by file/ { archive = 1; print; next }
        archive && /^$/ && members { archive = 0 }
        archive { print; if ($0 != "") members = 1; next }
        /^LOAD / { print }' "$map") >"$output/evidence/maps/$program.map"
    # Every static program takes members of the C library's archive.
    # A copy with no member means that the map has no archive section
    # under the heading that the awk program reads, as it would after
    # an ld release changes the heading. releases/sbom would read that
    # copy as a program that links no library.
    grep -q '^/.*\.a(' "$output/evidence/maps/$program.map" ||
        stop "the link map of $program names no archive member under the heading \"Archive member included to satisfy reference by file (symbol)\". Read $map, and change evidence.sh to the heading that this ld writes"
done
cp "$output/build/packages-file-list.txt" "$output/build/packages-file-list-staging.txt" \
    "$output/build/packages-file-list-host.txt" "$output/evidence/"
