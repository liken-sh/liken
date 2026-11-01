#!/usr/bin/env bash
#
# Report the inference runtime that the appearances image installs,
# and the newest release of each upstream.
#
# Dockerfile.appearances fetches four pins with ADD and checks each
# file against its --checksum:
#
#   - OpenVINO's runtime archive, from storage.openvinotoolkit.org.
#     Intel builds one archive of each release for each distribution
#     release, such as ubuntu24, and that name is the pin's channel.
#     The appearances image runs on the Debian sid of the bases, so the
#     newest Ubuntu archive is the newest channel. The release is the
#     newest that GitHub marks as not a pre-release, and
#     storage.openvinotoolkit.org/filetree.json lists its archives.
#   - Intel's OpenCL driver, intel-opencl-icd, from the newest release
#     of intel/compute-runtime on GitHub.
#   - gmmlib, libigdgmm12, which compute-runtime publishes in the same
#     release. It follows the compute-runtime pin.
#   - The graphics compiler, intel-igc-core-2 and intel-igc-opencl-2,
#     from intel/intel-graphics-compiler. Each compute-runtime release
#     names the compiler release that it was built with, and the pin
#     follows that release, not the compiler's newest one.
#
# A bump reads each checksum from the file or release text that
# upstream publishes beside the bytes, and writes the URL and the
# checksum into Dockerfile.appearances. Intel signs none of these
# files: storage.openvinotoolkit.org publishes each archive with a
# .sha256 file and no signature, compute-runtime publishes a ww<week>.sum
# file and no signature, and the compiler's sums are in its release
# text. So each digest rests on the HTTPS connection to its host
# alone. inference-closure.sh in
# appearances/ names the versioned library files of each runtime, so a
# bump stops at the Dockerfile and prints what to move by hand.
#
# Usage:
#   library-operator/latest.sh          report the four pins
#   library-operator/latest.sh --bump   write the newest OpenVINO
#                                       release of the newest channel
#   library-operator/latest.sh --bump compute-runtime
#                                       write the newest compute-runtime
#                                       release, its gmmlib, and the
#                                       compiler release it names

set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source-path=SCRIPTDIR source=../toolchains/pin.sh
source "$here/../toolchains/pin.sh"
dockerfile="$here/Dockerfile.appearances"

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

github() { github_api "repos/$1"; }

# url prints the one ADD source in the Dockerfile whose file name
# matches the pattern.
url() { grep -oE "https://[^ ]*/$1" "$dockerfile"; }

openvino_url="$(url 'openvino_toolkit_[^ ]*\.tgz')"
openvino_file="${openvino_url##*/}"
channel="$(sed -E 's/^openvino_toolkit_([a-z]+[0-9]+)_.*/\1/' <<<"$openvino_file")"
openvino="$(sed -E 's/^openvino_toolkit_[a-z]+[0-9]+_([0-9]+\.[0-9]+\.[0-9]+)\..*/\1/' <<<"$openvino_file")"
runtime="$(url 'intel-opencl-icd_[^ ]*\.deb' | sed -E 's|.*/intel-opencl-icd_([^-]+)-.*|\1|')"
gmmlib="$(url 'libigdgmm12_[^ ]*\.deb' | sed -E 's|.*/libigdgmm12_([^_]+)_.*|\1|')"
igc="$(url 'intel-igc-core-2_[^ ]*\.deb' | sed -E 's|.*/intel-igc-core-2_([^+]+)\+.*|\1|')"

# The newest OpenVINO release, and every Linux archive of it for
# x86_64 as "directory file". The directory of a release that ends in
# .0 drops that part, such as 2026.4 for 2026.4.0.
openvino_latest="$(github openvinotoolkit/openvino/releases/latest | jq -r .tag_name)" || openvino_latest=""
if [[ -n "$openvino_latest" ]] &&
    fetch_text https://storage.openvinotoolkit.org/filetree.json --max-time 300 |
    jq -r '.children[] | select(.name == "repositories") | .children[] | select(.name == "openvino")
            | .children[] | select(.name == "packages") | .children[] | .name as $d
            | .children[]? | select(.name == "linux") | .children[] | .name
            | select(test("_x86_64\\.tgz$")) | "\($d) \(.)"' |
    awk -v v="$openvino_latest" 'index($2, "_" v ".") > 0' >"$work/archives"; then
    newest_channel="$(awk '{ print $2 }' "$work/archives" | grep -oE '^openvino_toolkit_ubuntu[0-9]+' |
        sed 's/^openvino_toolkit_//' | sort -V | tail -1)" || newest_channel=""
else
    openvino_latest=""
    newest_channel=""
fi

github intel/compute-runtime/releases/latest >"$work/runtime.json" 2>/dev/null || echo '{}' >"$work/runtime.json"
runtime_latest="$(jq -r '.tag_name // empty' "$work/runtime.json")"
gmmlib_latest="$(jq -r '.assets[]?.name' "$work/runtime.json" |
    sed -nE 's/^libigdgmm12_([^_]+)_amd64\.deb$/\1/p')"
# The release text gives the compiler's packages as the URLs to
# download, one per line.
jq -r '.body // empty' "$work/runtime.json" |
    grep -oE 'https://github\.com/intel/intel-graphics-compiler/releases/download/[^ )]*\.deb' |
    sort -u >"$work/igc-urls" || true
igc_latest="$(sed -nE 's|.*/intel-igc-core-2_([^+]+)\+.*|\1|p' "$work/igc-urls")"

row openvino "$openvino" "$openvino_latest" "$channel" "${newest_channel:-?}" - \
    "OpenVINO's runtime archive in Dockerfile.appearances"
row compute-runtime "$runtime" "$runtime_latest" - - - \
    "Intel's OpenCL driver in Dockerfile.appearances"
row '  gmmlib' "$gmmlib" "$gmmlib_latest" - - - \
    "the libigdgmm12 of the compute-runtime release"
row '  igc' "$igc" "$igc_latest" - - - \
    "the graphics compiler release that compute-runtime names"

[[ "${1:-}" == "--bump" ]] || exit 0

# pin replaces the ADD source old with new, and the --checksum on the
# line above it with sha256.
pin() {
    local old="$1" new="$2" sha256="$3"
    [[ "$sha256" =~ ^[0-9a-f]{64}$ ]] || fail "no checksum for ${new##*/}"
    awk -v old="$old" -v new="$new" -v sum="$sha256" '
        index($0, old) {
            sub(/--checksum=sha256:[0-9a-f]+/, "--checksum=sha256:" sum, prev)
            i = index($0, old)
            $0 = substr($0, 1, i - 1) new substr($0, i + length(old))
        }
        NR > 1 { print prev }
        { prev = $0 }
        END { print prev }' "$dockerfile" >"$work/Dockerfile"
    cat "$work/Dockerfile" >"$dockerfile"
}

case "${2:-openvino}" in
openvino)
    [[ -n "$openvino_latest" ]] || fail "GitHub or storage.openvinotoolkit.org did not answer"
    [[ -n "$newest_channel" ]] || fail "OpenVINO $openvino_latest has no Ubuntu archive for x86_64"
    # The bump takes the newest channel as well as the newest release,
    # so a pin on an older Ubuntu archive moves even when its release
    # is current.
    [[ "$openvino_latest" != "$openvino" || "$newest_channel" != "$channel" ]] || {
        echo "openvino $openvino ($channel) is current"
        exit 0
    }
    read -r dir file < <(grep " openvino_toolkit_${newest_channel}_" "$work/archives")
    new="https://storage.openvinotoolkit.org/repositories/openvino/packages/$dir/linux/$file"
    sha256="$(fetch_text "$new.sha256" | awk -v f="$file" '$2 == f { print $1 }')"
    # appearances/README.md gives the archive's URL, its file names, and
    # its SHA-256, for a workstation that unpacks it by hand.
    old_sha256="$(grep -B1 -F "$openvino_url" "$dockerfile" | sed -nE 's/.*--checksum=sha256:([0-9a-f]{64}).*/\1/p')"
    pin "$openvino_url" "$new" "$sha256"
    sed -i "s|$openvino_file|$file|g; s|${openvino_file%.tgz}|${file%.tgz}|g; s|/packages/[^/]*/linux/|/packages/$dir/linux/|; s|$old_sha256|$sha256|g" \
        "$here/appearances/README.md"
    bumped openvino "$openvino ($channel)" "$openvino_latest ($newest_channel)"
    echo "the release's .sha256 file stands behind these bytes; Intel signs neither file"
    next_step "move the libopenvino and libtbb file names in appearances/inference-closure.sh,"
    echo "then docker buildx bake library-operator-appearances at the top of the repository"
    ;;
compute-runtime)
    bump_needed compute-runtime "$runtime" "$runtime_latest" || exit 0
    # The release's ww<week>.sum file lists the sum of each of its
    # packages, and the compiler's release text lists the sum of each
    # of its own. The release text has CRLF line ends.
    sums="$(jq -r '.assets[] | select(.name | test("^ww[0-9]+\\.sum$")) | .browser_download_url' "$work/runtime.json")"
    fetch_text "$sums" | tr -d '\r' >"$work/sums"
    github "intel/intel-graphics-compiler/releases/tags/v$igc_latest" | jq -r .body | tr -d '\r' >>"$work/sums"
    # Each line is "old new", for the four ADD sources.
    base="https://github.com/intel/compute-runtime/releases/download/$runtime_latest"
    for package in intel-opencl-icd libigdgmm12; do
        file="$(awk '{ print $2 }' "$work/sums" | grep -E "^${package}_.*_amd64\.deb$")"
        echo "$(url "${package}_[^ ]*\.deb") $base/$file"
    done >"$work/moves"
    for package in intel-igc-core-2 intel-igc-opencl-2; do
        echo "$(url "${package}_[^ ]*\.deb") $(grep "/${package}_" "$work/igc-urls")"
    done >>"$work/moves"
    # Every checksum is read before the first write, so a bump that
    # cannot find one leaves the Dockerfile as it was.
    while read -r old new; do
        sha256="$(awk -v f="${new##*/}" '$2 == f { print $1 }' "$work/sums")"
        [[ "$sha256" =~ ^[0-9a-f]{64}$ ]] || fail "no checksum for ${new##*/}"
        echo "$old $new $sha256"
    done <"$work/moves" >"$work/pins"
    while read -r old new sha256; do
        pin "$old" "$new" "$sha256"
    done <"$work/pins"
    bumped compute-runtime "$runtime" "$runtime_latest"
    bumped gmmlib "$gmmlib" "$gmmlib_latest"
    bumped igc "$igc" "$igc_latest"
    echo "the release's sum file and the compiler's release text stand behind these bytes; Intel signs neither"
    next_step "move the libigdgmm and libigc file names in appearances/inference-closure.sh,"
    echo "then docker buildx bake library-operator-appearances at the top of the repository"
    ;;
*) fail "no pin named $2; try openvino or compute-runtime" ;;
esac
