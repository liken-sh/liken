# shellcheck shell=bash
#
# The steps that every fetch.sh and latest.sh in the repository takes:
# check for its tools, read an upstream, download a pinned file and
# check its digest, look up a digest in a table, print a row of the
# `make versions` report, and write a bump into the files that hold
# the pin. Root AGENTS.md states the rules of pins and downloads, and
# each rule has one implementation here, so a fix to a step reaches
# every pin at once. `ci pins` fails a fetch.sh or a latest.sh that
# does not source this file, or that runs curl or wget itself.
#
# A script sources this file by its path from the script's own
# directory, after `set -euo pipefail`:
#
#   here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
#   # shellcheck source-path=SCRIPTDIR source=../../toolchains/pin.sh
#   source "$here/../../toolchains/pin.sh"
#
# The directive lets shellcheck -x, which the component's prek hook
# runs, read this file from the script's directory, so it sees the
# functions that the script calls.
#
# Each message that a function prints names the script that called
# it, as in "fetch.sh: missing required tool: zstd".

pin_script="$(basename "$0")"
pin_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

# fail <message>: print the message on stderr and stop the script.
fail() {
    echo "$pin_script: $*" >&2
    exit 1
}

# require_tools <tool>...: stop when a tool is not installed. curl is
# always required, because every network read goes through it.
require_tools() {
    local tool
    for tool in curl "$@"; do
        command -v "$tool" >/dev/null || fail "missing required tool: $tool"
    done
}

# --- reading an upstream -------------------------------------------------

# http [curl option]... <url>: one request, with the retries and the
# time limits that every read of an upstream takes. A server that does
# not answer in 20 seconds, or a request that takes more than a
# minute, fails, so that one slow upstream cannot hold up
# `make versions`. A caller passes --max-time to allow a larger file.
http() {
    curl -sS --retry 3 --connect-timeout 20 --max-time 60 "$@"
}

# fetch_text <url> [curl option]...: print what the URL serves, and
# fail on an HTTP error.
fetch_text() {
    local url="$1"
    shift
    http -fL "$@" "$url"
}

# url_exists <url>: true when the URL answers with a success.
url_exists() {
    http -fL -o /dev/null "$1" 2>/dev/null
}

# fetch_file <url> <path>: download a file whose digest the repository
# does not hold yet, such as a checksum file or a release that a bump
# is about to pin. The bytes go to a .partial file that takes the name
# only when the transfer completes, so a broken transfer never leaves
# a file under the name that a later step reads.
fetch_file() {
    local url="$1" path="$2"
    mkdir -p "$(dirname "$path")"
    http -fL --max-time 600 -o "$path.partial" "$url" || {
        rm -f "$path.partial"
        fail "$url did not answer"
    }
    mv "$path.partial" "$path"
}

# digest_of_url <url>: print the sha256 of what the URL serves. A bump
# calls it for an upstream that publishes no checksum, so the digest
# comes from the bump's own download, and the bump's message says so.
digest_of_url() {
    local digest
    digest="$(http -fL --max-time 600 "$1" | sha256sum | cut -d' ' -f1)" ||
        fail "$1 did not answer"
    echo "$digest"
}

# newest_tag <repository URL> <extended regex> [prefix]: print the
# newest tag of a git repository that matches the regex, after the
# prefix is removed. A tag query through git needs no token and has no
# rate limit, and a release that a pin fetches is named by its tag.
newest_tag() {
    local prefix="${3:-}"
    timeout 60 git ls-remote --tags --refs "$1" |
        sed "s|.*refs/tags/$prefix||" |
        grep -E "$2" |
        sort -V | tail -1
}

# tag_commit <repository URL> <tag>: print the commit that a tag of a
# git repository names. An annotated tag is an object of its own, and
# git lists the commit under it as the tag with ^{} appended, so the
# commit comes from that line when it exists. A pin that names the tag
# object would not match the commit that a checkout of the tag holds.
tag_commit() {
    local refs commit
    refs="$(timeout 60 git ls-remote --tags "$1" "refs/tags/$2" "refs/tags/$2^{}")" ||
        fail "$1 did not answer"
    commit="$(awk -v t="refs/tags/$2^{}" '$2 == t { print $1 }' <<<"$refs")"
    [[ -n "$commit" ]] || commit="$(awk -v t="refs/tags/$2" '$2 == t { print $1 }' <<<"$refs")"
    [[ "$commit" =~ ^[0-9a-f]{40}$ ]] || fail "$1 has no tag $2"
    echo "$commit"
}

# github_token: print the token for the GitHub API, from GITHUB_TOKEN
# or from the gh CLI, or nothing. The API allows 60 anonymous requests
# an hour, which one run of `make versions` can use up.
github_token() {
    echo "${GITHUB_TOKEN:-$(gh auth token 2>/dev/null || true)}"
}

# github_api <path> [curl option]...: print what the GitHub API serves
# at the path, with the token when one is at hand.
github_api() {
    local path="$1" token
    shift
    token="$(github_token)"
    if [[ -n "$token" ]]; then
        fetch_text "https://api.github.com/$path" -H "Authorization: Bearer $token" "$@"
    else
        fetch_text "https://api.github.com/$path" "$@"
    fi
}

# registry <repository> <path> [curl option]...: one request to the
# registry of an image repository. The repository can carry a host, and
# a bare name is an official image on Docker Hub. A registry that
# answers 401 names its token service in WWW-Authenticate, and an
# anonymous pull token from there reads any public image, so a report
# needs no docker daemon and no login.
registry() {
    local repo="$1" path="$2" host=registry-1.docker.io name="$1" url auth realm service token
    shift 2
    if [[ "${repo%%/*}" == *.* ]]; then
        host="${repo%%/*}"
        name="${repo#*/}"
    elif [[ "$repo" != */* ]]; then
        name="library/$repo"
    fi
    url="https://$host/v2/$name/$path"
    auth="$(http -o /dev/null -D - --max-time 20 "$url" | tr -d '\r' |
        grep -i '^www-authenticate:' || true)"
    if [[ -z "$auth" ]]; then
        fetch_text "$url" "$@"
        return
    fi
    realm="$(sed -n 's/.*realm="\([^"]*\)".*/\1/p' <<<"$auth")"
    service="$(sed -n 's/.*service="\([^"]*\)".*/\1/p' <<<"$auth")"
    token="$(fetch_text "$realm?service=$service&scope=repository:$name:pull" --max-time 20 |
        jq -r '.token // .access_token')"
    fetch_text "$url" -H "Authorization: Bearer $token" "$@"
}

# registry_digest <repository> <tag>: print the digest that a tag names
# today. The Accept header lists the index types first, so a
# multi-platform tag answers with the digest of its index, which is
# what a pin names.
registry_digest() {
    registry "$1" "manifests/$2" -I \
        -H 'Accept: application/vnd.oci.image.index.v1+json, application/vnd.docker.distribution.manifest.list.v2+json, application/vnd.oci.image.manifest.v1+json, application/vnd.docker.distribution.manifest.v2+json' |
        tr -d '\r' | awk 'tolower($1) == "docker-content-digest:" { print $2 }'
}

# --- downloading a pinned file -------------------------------------------

# digest_matches <digest> <path>: true when the file exists and its
# sha256, or its sha512 for a digest of 128 characters, is the digest.
digest_matches() {
    local digest="$1" path="$2" sum
    case "${#digest}" in
    64) sum=sha256sum ;;
    128) sum=sha512sum ;;
    *) fail "$digest is not a sha256 or a sha512 digest" ;;
    esac
    [[ -f "$path" ]] && "$sum" --check --status <<<"$digest  $path" 2>/dev/null
}

# fetch_verified [--resume] <url> <digest> <path>: put the file that
# the digest names at the path.
#
# The digest is the one that the calling script pins: from its
# manifest through fetch_pinned, or from a `declare -A` table that
# holds it beside the version it belongs to, as in
# "${buildroot_sha256[$version]:-}", in a pin that no release ships.
# latest.sh --bump writes it, so a fetch never reads a digest from
# upstream. An empty digest means that the script pins none for this
# version, and the fetch stops.
#
# A cached file that matches the digest is used as it is. Anything
# else, a missing, torn, or changed file, is downloaded again into a
# .partial file, which takes the name only after its digest matches.
# A .partial that does not match is deleted and the script stops, so a
# bad download never stays in the cache to fail every later run.
#
# --resume is for a large file. Each retry continues from the bytes
# already in the .partial file, so a stream that breaks again and again
# still finishes. The delete matters then: curl answers a request to
# resume a .partial file that is already full length with success,
# and leaves the bytes as they are. Without --resume, each download
# starts from the first byte.
#
# A transfer that moves less than 1 KB a second for a minute fails and
# retries. A limit on the whole transfer would fail a large file on a
# slow link, and this limit fails only a transfer that stopped.
fetch_verified() {
    local resume=() progress=(-sS)
    if [[ "$1" == --resume ]]; then
        resume=(-C -)
        shift
    fi
    local url="$1" digest="$2" path="$3"
    [[ -n "$digest" ]] || fail "no digest is pinned for $url; $(pin_dir)/latest.sh --bump writes it"
    digest_matches "$digest" "$path" && return
    mkdir -p "$(dirname "$path")"
    ((${#resume[@]})) || rm -f "$path.partial"
    [[ -t 2 ]] && progress=(--progress-bar)
    echo "downloading $url" >&2
    curl -fL "${progress[@]}" --retry 5 --retry-all-errors --retry-delay 5 \
        --connect-timeout 30 --speed-limit 1024 --speed-time 60 \
        "${resume[@]}" -o "$path.partial" "$url"
    digest_matches "$digest" "$path.partial" || {
        rm -f "$path.partial"
        fail "the bytes at $url do not match the pinned digest $digest"
    }
    mv "$path.partial" "$path"
}

# pin_dir: print the directory of the calling script, from the top of
# the repository.
pin_dir() {
    local dir
    dir="$(cd "$(dirname "$0")" && pwd)"
    echo "${dir#"$pin_root"/}"
}

# --- the manifest --------------------------------------------------------

# Each pin whose files a release or an image ships holds a manifest,
# pin.toml, in its directory. The manifest has one table for each file
# that the fetch or the Dockerfile downloads, and the table holds what
# an SBOM needs to name the file: its version, url, sha256, purl, and
# the SPDX expression of its license, and an optional CycloneDX scope,
# CycloneDX component type, and CPE 2.3 name:
#
#   [k3s]
#   version = "v1.37.1+k3s1"
#   url = "https://github.com/k3s-io/k3s/releases/download/v1.37.1%2Bk3s1/k3s"
#   sha256 = "92b9..."
#   purl = "pkg:github/k3s-io/k3s@v1.37.1%2Bk3s1"
#   license = "Apache-2.0"
#
# latest.sh --bump writes the version, url, sha256, purl, and CPE with
# set_pin, fetch.sh downloads with fetch_pinned, and the source mirror,
# the image's components record, and the release document read a field
# with pin_value. So the repository holds each digest once, and the
# SBOM reads the same bytes that the build checked. A Dockerfile that
# fetches the file with ADD --checksum keeps a copy of the digest,
# because BuildKit reads it from the Dockerfile's text, and the same
# bump writes it. toolchains/manifest gives each entry as a CycloneDX
# component, for the SBOM of a release and for the record that an
# image carries. A person writes the license, the scope, and the type,
# which a bump leaves as they are. A person also writes backports, the
# space-separated GitHub URLs of the upstream commits that a build
# applies to the release, which the SBOM states as the component's
# pedigree, and removes them when the bump reaches a release that holds
# the commits. `ci pins`
# fails a pin that ships with no manifest, an entry with a missing
# field, and a manifest at another version than the pin's VERSION. The
# reader is the shell, so every value is a string on one line.

# pin_manifest: print the path of the manifest beside the calling
# script.
pin_manifest() {
    echo "$(cd "$(dirname "$0")" && pwd)/pin.toml"
}

# pin_value <entry> <field> [<manifest>]: print one field of one entry
# of a manifest, the one beside the calling script unless the call
# names another, and stop when the entry has no such field.
pin_value() {
    local entry="$1" field="$2" manifest="${3:-$(pin_manifest)}" value
    [[ -f "$manifest" ]] || fail "${manifest#"$pin_root"/} does not exist"
    value="$(PIN_ENTRY="[$entry]" PIN_FIELD="$field" awk '
        /^\[/ { inside = ($0 == ENVIRON["PIN_ENTRY"]) }
        inside && $1 == ENVIRON["PIN_FIELD"] && $2 == "=" {
            sub(/^[^=]*= *"/, ""); sub(/" *$/, ""); print; exit
        }' "$manifest")"
    [[ -n "$value" ]] || fail "${manifest#"$pin_root"/} names no $field for [$entry]"
    echo "$value"
}

# fetch_pinned [--resume] <entry> <version> <path> [<manifest>]: put
# the file that an entry of a manifest names at the path, through
# fetch_verified, with the entry's url and sha256. The manifest is the
# one beside the calling script unless the call names another, as the
# source mirror names the manifest of each pin whose source it offers.
# The version is the one that the caller builds or mirrors, from the
# pin's VERSION file, and the fetch stops when the manifest names
# another, because the files that a build extracts must be the ones
# that the SBOM lists.
fetch_pinned() {
    local resume=()
    if [[ "$1" == --resume ]]; then
        resume=(--resume)
        shift
    fi
    local entry="$1" version="$2" path="$3" manifest="${4:-$(pin_manifest)}" pinned url digest dir
    dir="$(dirname "${manifest#"$pin_root"/}")"
    pinned="$(pin_value "$entry" version "$manifest")"
    [[ "$pinned" == "$version" ]] ||
        fail "$dir/pin.toml names $entry $pinned, and this fetch builds $version; $dir/latest.sh --bump writes both"
    url="$(pin_value "$entry" url "$manifest")"
    digest="$(pin_value "$entry" sha256 "$manifest")"
    fetch_verified "${resume[@]}" "$url" "$digest" "$path"
}

# set_pin <entry> <version> <url> <sha256> <purl> [<cpe>]: write a bump
# into one entry of the manifest beside the calling script. The entry
# and each field must exist, so a bump never writes half an entry. A
# CPE names the version, so the bump of an entry that has one writes
# the new CPE too.
set_pin() {
    local manifest
    manifest="$(pin_manifest)"
    set_toml "$manifest" "$1" version "\"$2\""
    set_toml "$manifest" "$1" url "\"$3\""
    set_toml "$manifest" "$1" sha256 "\"$4\""
    set_toml "$manifest" "$1" purl "\"$5\""
    if [[ -n "${6:-}" ]]; then
        set_toml "$manifest" "$1" cpe "\"$6\""
    fi
}

# cpe_version <version>: print the part of a release tag or a package
# version that is the upstream's own version: the text after a leading
# v and before the first + or -. NVD lists k3s v1.37.1+k3s1 as 1.37.1
# and Ubuntu's GRUB 2.14-2ubuntu3 as 2.14, and a scanner matches a CPE
# only at the version that NVD's records name. upstreamVersion in
# ci/manifests.go is the same rule for the check.
cpe_version() {
    local version="${1#v}"
    version="${version%%[+-]*}"
    echo "$version"
}

# purl_escape <text>: percent-encode text for the version of a package
# URL, so the + of v1.37.1+k3s1 reads %2B. jq's @uri keeps the
# characters that the purl specification leaves bare.
purl_escape() {
    jq -rn --arg text "$1" '$text | @uri'
}

# generic_purl <name> <version> <url> <sha256>: print the package URL
# of a file that no package ecosystem names. The purl specification
# identifies such a file by the URL it downloads from and its digest.
generic_purl() {
    echo "pkg:generic/$1@$(purl_escape "$2")?download_url=$3&checksum=sha256:$4"
}

# github_purl <owner/repository> <tag>: print the package URL of a
# release or a tagged tree of a GitHub repository. The specification
# lowercases the owner and the name.
github_purl() {
    echo "pkg:github/${1,,}@$(purl_escape "$2")"
}

# deb_purl <distribution> <package> <version> [<arch>]: print the
# package URL of a package of a Debian or Ubuntu archive, an amd64
# package unless the call names another architecture. The purl
# specification names a source package with the architecture source.
deb_purl() {
    echo "pkg:deb/$1/$2@$(purl_escape "$3")?arch=${4:-amd64}"
}

# --- the build container -------------------------------------------------

# container_runtime: print docker or podman, whichever is installed,
# or nothing.
container_runtime() {
    local candidate
    for candidate in docker podman; do
        if command -v "$candidate" >/dev/null; then
            echo "$candidate"
            return
        fi
    done
}

# image_of <key>: print the reference that toolchains/images.toml names
# for the key, and stop when it names none. images.toml names Docker
# Hub's images in the short form, and podman needs the registry in
# full, so a reference without a registry gets docker.io and, for an
# official image, library/.
image_of() {
    local ref host
    ref="$(awk -F' = ' -v k="$1" '$1 == k { gsub(/"/, "", $2); print $2 }' \
        "$pin_root/toolchains/images.toml")"
    [[ -n "$ref" ]] || fail "toolchains/images.toml names no $1 image"
    host="${ref%%/*}"
    if [[ "$ref" != */* ]]; then
        ref="docker.io/library/$ref"
    elif [[ "$host" != *.* && "$host" != *:* ]]; then
        ref="docker.io/$ref"
    fi
    echo "$ref"
}

# --- signatures ----------------------------------------------------------

# verify_signature [--signer <fingerprint>] <key.asc> <signature> [<file>]
#
# Check a detached signature of a file, or, with no file, a clearsigned
# document, against the armored key. For a clearsigned document the
# function prints the signed text alone, so a caller reads its digest
# only from lines that the signature covers, and never from text that
# someone added around the signed block. --signer accepts a signature
# only from the key with that fingerprint, when the key file holds
# more than one key or a bump must name the one signer it trusts.
#
# A file can carry signatures from more than one key. Launchpad signs a
# PPA's InRelease with the PPA's current key and with the key it used
# before, and the repository holds only the current one. gpgv fails
# when it lacks the key of any signature, so with --signer the function
# reads gpgv's status lines in place of its exit code: it accepts the
# file when the named key made a valid signature and no signature is
# bad, expired, or revoked. apt accepts such a file the same way, on
# one valid signature from a key in Signed-By.
#
# gpgv reads a binary keyring, and the repository keeps each key in
# its armored form, which a person can read and compare against the
# fingerprint that the upstream publishes.
verify_signature() {
    local signer=""
    if [[ "$1" == --signer ]]; then
        signer="$2"
        shift 2
    fi
    local key="$1" signature="$2" file="${3:-}" work status=0
    command -v gpg >/dev/null || fail "missing required tool: gpg"
    command -v gpgv >/dev/null || fail "missing required tool: gpgv"
    work="$(mktemp -d)"
    gpg --dearmor <"$key" >"$work/key.gpg"
    if [[ -n "$file" ]]; then
        gpgv --status-fd 3 --keyring "$work/key.gpg" "$signature" "$file" \
            3>"$work/status" >&2 || status=1
    else
        gpgv --status-fd 3 --keyring "$work/key.gpg" --output "$work/signed" \
            "$signature" 3>"$work/status" >&2 || status=1
    fi
    if [[ -n "$signer" ]]; then
        status=0
        awk -v f="$signer" '
            $2 == "VALIDSIG" && $NF == f { found = 1 }
            $2 ~ /^(BADSIG|EXPSIG|EXPKEYSIG|REVKEYSIG)$/ { bad = 1 }
            END { exit !(found && !bad) }' "$work/status" || status=2
    fi
    [[ -n "$file" || "$status" != 0 ]] || cat "$work/signed"
    rm -rf "$work"
    case "$status" in
    1) fail "$signature does not verify against ${key#"$pin_root"/}" ;;
    2) fail "$signature is not signed by $signer" ;;
    esac
}

# --- Debian and Ubuntu archives ------------------------------------------

# archive_index <key.asc> <suite URL> <index> <path>: put one index of
# a Debian or Ubuntu suite, such as main/binary-amd64/Packages.xz or
# main/source/Sources.xz, at the path, after it matches the digest that
# the suite's signed InRelease file lists for it. archive_deb and
# archive_source read the digest of each file that they pin from the
# index, so the signature on InRelease covers that digest too.
#
# An InRelease file that names a Valid-Until date in the past stops
# the function, as it stops apt, so a mirror cannot serve an old signed
# index that still names a superseded package.
archive_index() {
    local key="$1" suite="$2" index="$3" path="$4" valid digest
    command -v xz >/dev/null || fail "missing required tool: xz"
    fetch_file "$suite/InRelease" "$path.InRelease"
    verify_signature "$key" "$path.InRelease" >"$path.Release"
    valid="$(sed -n 's/^Valid-Until: //p' "$path.Release")"
    if [[ -n "$valid" ]] && (($(date -d "$valid" +%s) < $(date +%s))); then
        fail "$suite/InRelease expired at $valid"
    fi

    # The digest sections of a Release file start with a line such as
    # "SHA256:", and each entry under one is "<digest> <size> <path>".
    # SHA256 is the first choice, and Ubuntu's development series lists
    # SHA512 alone.
    digest="$(awk -v idx="$index" '
        /^[^ ]/ { section = $1 }
        section == "SHA256:" && $3 == idx { sha256 = $1 }
        section == "SHA512:" && $3 == idx { sha512 = $1 }
        END { print (sha256 != "" ? sha256 : sha512) }' "$path.Release")"
    [[ -n "$digest" ]] || fail "$suite/InRelease lists no $index"
    fetch_verified "$suite/$index" "$digest" "$path"
}

# archive_deb<key.asc> <suite URL> <index> <package> [<version>]
#
# Print "<version> <sha256>" for one binary package in a Debian or
# Ubuntu archive, such as
# archive_deb key.asc https://deb.debian.org/debian/dists/sid
# main/binary-all/Packages.xz ovmf-generic. With no version, the
# function takes the newest version that the index lists.
#
# The archive signs one file per suite, InRelease, and signs no
# package. InRelease lists the digest of each Packages index, and each
# Packages index lists the digest of each package. So the function
# verifies InRelease against the key, checks the index against the
# digest that the signed text lists, and checks the package against
# the digest that the index lists. A change to any of the three files
# on a mirror or in transit stops the bump. The key file holds every
# key that signs the suite, because gpgv fails on a signature whose
# key it does not have, and Debian signs each suite with the keys of
# two releases.
#
# The function downloads the package and prints the sha256 of the
# verified bytes, because a fetch.sh pins a sha256, and the Packages
# indexes of Ubuntu's development series list only a SHA512.
archive_deb() (
    local key="$1" suite="$2" index="$3" package="$4" version="${5:-}"
    local work record deb_digest
    work="$(mktemp -d)"
    trap 'rm -rf "$work"' EXIT

    archive_index "$key" "$suite" "$index" "$work/Packages.xz"

    # One record per package version: "<version> <filename> <digest>".
    record="$(xz -dc "$work/Packages.xz" | awk -v p="$package" -v v="$version" '
        /^Package: / { name = $2; ver = ""; file = ""; sha256 = ""; sha512 = "" }
        /^Version: / { ver = $2 }
        /^Filename: / { file = $2 }
        /^SHA256: / { sha256 = $2 }
        /^SHA512: / { sha512 = $2 }
        /^$/ && name == p && (v == "" || ver == v) {
            print ver, file, (sha256 != "" ? sha256 : sha512)
        }
        END {
            if (name == p && (v == "" || ver == v))
                print ver, file, (sha256 != "" ? sha256 : sha512)
        }' | sort -V | tail -1)"
    [[ -n "$record" ]] || fail "$suite/$index lists no $package ${version:-at any version}"
    read -r version file deb_digest <<<"$record"

    fetch_verified "${suite%/dists/*}/$file" "$deb_digest" "$work/package.deb"
    echo "$version $(sha256sum "$work/package.deb" | cut -d' ' -f1)"
)

# archive_source <key.asc> <suite URL> <index> <source package> <version>
#
# Print "<file> <sha256>" for each file of one version of a source
# package in a Debian or Ubuntu archive, such as the upstream tarball,
# the packaging tarball, and the .dsc file. The index is the suite's
# Sources index, such as main/source/Sources.xz, and its record of the
# version lists the digest of each file. The chain of digests is the
# one that archive_deb follows, so the files that the function prints
# are the ones that the archive's key covers. It downloads each file
# from the archive's pool and prints the sha256 of the verified bytes,
# because the Sources indexes of Ubuntu's development series list only
# a SHA512.
archive_source() (
    local key="$1" suite="$2" index="$3" package="$4" version="$5"
    local work files dir file digest
    work="$(mktemp -d)"
    trap 'rm -rf "$work"' EXIT

    archive_index "$key" "$suite" "$index" "$work/Sources.xz"

    # A record of a Sources index is a paragraph. Each file is a line of
    # the field whose name ends in -Sha256 or -Sha512, as
    # " <digest> <size> <file>", and Directory names the file's
    # directory in the pool. One line per file: "<dir> <file> <digest>".
    files="$(xz -dc "$work/Sources.xz" | awk -v p="$package" -v v="$version" '
        BEGIN { RS = ""; FS = "\n" }
        {
            name = ""; ver = ""; dir = ""; section = ""; n = 0; m = 0
            for (i = 1; i <= NF; i++) {
                line = $i
                if (line ~ /^[^ ]/) section = substr(line, 1, index(line, ":"))
                if (line ~ /^Package: /) name = substr(line, 10)
                else if (line ~ /^Version: /) ver = substr(line, 10)
                else if (line ~ /^Directory: /) dir = substr(line, 12)
                else if (line ~ /^ / && section ~ /-Sha256:$/) { split(line, f, " "); s256[++n] = f[3] " " f[1] }
                else if (line ~ /^ / && section ~ /-Sha512:$/) { split(line, f, " "); s512[++m] = f[3] " " f[1] }
            }
            if (name == p && ver == v) {
                if (n > 0) for (i = 1; i <= n; i++) print dir, s256[i]
                else for (i = 1; i <= m; i++) print dir, s512[i]
                exit
            }
        }')"
    [[ -n "$files" ]] || fail "$suite/$index lists no $package $version"

    while read -r dir file digest; do
        fetch_verified "${suite%/dists/*}/$dir/$file" "$digest" "$work/$file"
        echo "$file $(sha256sum "$work/$file" | cut -d' ' -f1)"
    done <<<"$files"
)

# ubuntu_api is the root of Launchpad's API for the Ubuntu
# distribution, which names its series and what each one publishes.
ubuntu_api="https://api.launchpad.net/devel/ubuntu"

# launchpad_published <source package> [curl option]...: print
# "<version> <series> <pocket>" for each publication of the source
# package in Ubuntu's primary archive, newest series first. The
# options narrow the query, as in --data status=Published. The
# Proposed pocket holds builds that wait for their tests, so they are
# not yet in the series, and the function leaves them out. The API
# answers JSON, and the function reads the two fields it needs with
# grep. A record's display_name reads like "grub2 2.14-2ubuntu3 in
# tenacious", and its pocket follows it in the same record.
launchpad_published() {
    local source="$1"
    shift
    fetch_text "$ubuntu_api/+archive/primary" --max-time 120 -G \
        --data ws.op=getPublishedSources --data "source_name=$source" \
        --data exact_match=true "$@" |
        grep -oE '"display_name": "[^"]*"|"pocket": "[^"]*"' |
        paste - - |
        grep -v '"pocket": "Proposed"' |
        sed -E 's|^"display_name": "[^ ]+ ([^ ]+) in ([^"]+)"\t"pocket": "([^"]+)"$|\1 \2 \3|'
}

# ubuntu_development_series: print the series that Launchpad names as
# Ubuntu's current one, which is the development series.
ubuntu_development_series() {
    fetch_text "$ubuntu_api" |
        grep -oE '"current_series_link": "[^"]*"' |
        sed -e 's|.*/||' -e 's|"$||'
}

# ubuntu_newest_in <source package> <series>: print the newest version
# of the source package that the series publishes now.
ubuntu_newest_in() {
    launchpad_published "$1" --data status=Published --data "distro_series=$ubuntu_api/$2" |
        cut -d' ' -f1 | sort -V | tail -1
}

# ubuntu_suite <source package> <version>: print the URL of the suite
# of Ubuntu's archive that publishes the version of the source package.
# Launchpad names the series and the pocket, and each pocket other
# than Release is a suite of its own, such as resolute-updates.
ubuntu_suite() {
    local series pocket suite
    read -r _ series pocket < <(launchpad_published "$1" \
        --data status=Published --data "version=$2" | head -1) ||
        fail "Launchpad names no series that publishes $1 $2"
    suite="$series"
    [[ "$pocket" == Release ]] || suite="$series-${pocket,,}"
    echo "http://archive.ubuntu.com/ubuntu/dists/$suite"
}

# ubuntu_deb_digest <source package> <binary package> <component>
# <version>: print the sha256 of an amd64 package of Ubuntu's archive
# that one Ubuntu source package builds, through the signed InRelease
# of the suite that publishes the version. trust/keys/ubuntu-archive.asc
# holds the key that signs the archive.
ubuntu_deb_digest() {
    local source="$1" package="$2" component="$3" version="$4" suite
    suite="$(ubuntu_suite "$source" "$version")"
    archive_deb "$pin_root/trust/keys/ubuntu-archive.asc" "$suite" \
        "$component/binary-amd64/Packages.xz" "$package" "$version" |
        cut -d' ' -f2
}

# launchpad_sourcefiles is the root of the URLs at which Launchpad
# serves each file of each version of an Ubuntu source package. The
# archive's pool deletes a version when a newer one replaces it, and
# Launchpad keeps every version, so the source mirror downloads from
# Launchpad.
launchpad_sourcefiles="https://launchpad.net/ubuntu/+archive/primary/+sourcefiles"

# ubuntu_source_files <source package> <component> <version>: print
# "<kind> <file> <sha256>" for each file of a version of an Ubuntu
# source package, through the signed InRelease of the suite that
# publishes it. The kind is orig for the upstream tarball, debian for
# the packaging tarball, and dsc for the .dsc file. The function stops
# when one of the three is missing or when the package holds a file of
# another kind, such as a second upstream tarball, because the source
# mirror offers every file and a bump must not drop one. It leaves out
# an upstream signature, a .asc file, which the mirror does not offer.
ubuntu_source_files() {
    local source="$1" component="$2" version="$3" suite files file digest kind kinds=""
    suite="$(ubuntu_suite "$source" "$version")"
    files="$(archive_source "$pin_root/trust/keys/ubuntu-archive.asc" "$suite" \
        "$component/source/Sources.xz" "$source" "$version")"
    while read -r file digest; do
        case "$file" in
        *.asc) continue ;;
        *.orig.tar.*) kind=orig ;;
        *.debian.tar.*) kind=debian ;;
        *.dsc) kind=dsc ;;
        *) fail "$source $version holds $file, which is not an upstream tarball, a packaging tarball, or a .dsc file" ;;
        esac
        [[ " $kinds " != *" $kind "* ]] || fail "$source $version holds more than one $kind file"
        kinds="$kinds $kind"
        echo "$kind $file $digest"
    done <<<"$files"
    [[ " $kinds " == *" orig "* && " $kinds " == *" debian "* && " $kinds " == *" dsc "* ]] ||
        fail "$source $version holds no upstream tarball, packaging tarball, or .dsc file"
}

# set_ubuntu_source_pins <entry prefix> <source package> <version>
# <files>: write the files that ubuntu_source_files printed into the
# entries <prefix>-orig, <prefix>-debian, and <prefix>-dsc of the
# manifest beside the calling script, at their Launchpad URLs.
set_ubuntu_source_pins() {
    local prefix="$1" source="$2" version="$3" kind file digest
    while read -r kind file digest; do
        set_pin "$prefix-$kind" "$version" "$launchpad_sourcefiles/$source/$version/$file" \
            "$digest" "$(deb_purl ubuntu "$source" "$version" source)"
    done <<<"$4"
}

# --- the report ----------------------------------------------------------

# row <name> <pinned> <latest> <channel> <newest channel> <hold> <note>
#
# Print one row of the report that versions.sh reads: seven columns,
# separated by tabs. An empty latest prints `?`, because it means the
# upstream did not answer. A sub-pin's name starts with two spaces, and
# its row follows the row of the pin it belongs to. Root plan 83 and
# the bump-components skill give the meaning of each column.
row() {
    (($# == 7)) || fail "a report row has 7 columns, and this one has $#: $*"
    printf '%s\t%s\t%s\t%s\t%s\t%s\t%s\n' "$1" "$2" "${3:-?}" "$4" "$5" "$6" "$7"
}

# --- the bump ------------------------------------------------------------

# bump_needed <name> <pinned> <latest>: stop when the upstream did not
# answer, and print that the pin is current and return false when it
# is. A latest.sh with one pin calls it as `bump_needed ... || exit 0`.
bump_needed() {
    [[ -n "$3" ]] || fail "the upstream of $1 did not answer"
    if [[ "$2" == "$3" ]]; then
        echo "$1 $2 is current"
        return 1
    fi
}

# bumped <name> <old> <new>: print the line that says what a bump moved.
bumped() {
    echo "$1: $2 -> $3"
}

# next_step <text>: print the line that names what to run after a bump.
next_step() {
    echo "next: $*"
}

# set_line <file> <extended regex> <line>: replace the one line of the
# file that matches the regex. A bump that matched no line, or more
# than one, stops, because a pattern that silently matched nothing
# would report a bump that did not happen. The file keeps its mode.
set_line() {
    local file="$1" count
    count="$(PIN_RE="$2" awk '$0 ~ ENVIRON["PIN_RE"] { n++ } END { print n + 0 }' "$file")"
    ((count == 1)) || fail "${file#"$pin_root"/} has $count lines that match $2, not 1"
    PIN_RE="$2" PIN_LINE="$3" awk '$0 ~ ENVIRON["PIN_RE"] { print ENVIRON["PIN_LINE"]; next } { print }' \
        "$file" >"$file.pin"
    cat "$file.pin" >"$file"
    rm "$file.pin"
}

# var_of <file> <name>: print the value of a pin that a fetch.sh holds
# as `name="value"` at the start of a line, so a latest.sh reads the
# pin from there and keeps no second copy of it.
var_of() {
    sed -n "s|^$2=\"\([^\"]*\)\"$|\1|p" "$1"
}

# set_var <file> <name> <value>: write `name="value"` over the line that
# assigns the variable at the start of a line.
set_var() {
    set_line "$1" "^$2=\"[^\"]*\"$" "$2=\"$3\""
}

# set_toml <file> <section> <key> <value>: write `key = value` over the
# one line that sets the key in the [section] of a TOML file, such as
# the version of a package.toml or an entry of its
# [package.upstream]. The value is TOML, so a string carries its
# quotes: set_toml package.toml package version '"20261101"'.
set_toml() {
    local file="$1"
    PIN_SECTION="[$2]" PIN_KEY="$3" PIN_LINE="$3 = $4" awk '
        /^\[/ { inside = ($0 == ENVIRON["PIN_SECTION"]) }
        inside && $1 == ENVIRON["PIN_KEY"] && $2 == "=" { print ENVIRON["PIN_LINE"]; n++; next }
        { print }
        END { exit n != 1 }' "$file" >"$file.pin" || {
        rm -f "$file.pin"
        fail "${file#"$pin_root"/} has no one $3 in [$2]"
    }
    cat "$file.pin" >"$file"
    rm "$file.pin"
}

# set_entry <file> <table> <key> <value>: write the one entry of a
# `declare -A` table that pins only the current version. A table that
# holds more than one entry stops the bump, because such a table keeps
# old versions and takes add_entry.
set_entry() {
    edit_table "$@" replace
}

# add_entry <file> <table> <key> <value>: add an entry at the top of a
# `declare -A` table that keeps the entries of older versions, or
# replace the key's entry when the table has one. An older checkout
# still names its own version, and a table that forgets it is a build
# that cannot be repeated.
add_entry() {
    edit_table "$@" add
}

# edit_table <file> <table> <key> <value> <replace|add>: the edit that
# set_entry and add_entry make. The entries of a table are the lines
# between `declare -A <table>=(` and the line `)`.
edit_table() {
    local file="$1"
    PIN_TABLE="declare -A $2=(" PIN_KEY="    [\"$3\"]=" PIN_LINE="    [\"$3\"]=\"$4\"" PIN_MODE="$5" awk '
        $0 == ENVIRON["PIN_TABLE"] { print; print ENVIRON["PIN_LINE"]; tables++; inside = 1; next }
        inside && $0 == ")" { inside = 0 }
        inside { entries++ }
        inside && (ENVIRON["PIN_MODE"] == "replace" || index($0, ENVIRON["PIN_KEY"]) == 1) { next }
        { print }
        END { exit !(tables == 1 && (ENVIRON["PIN_MODE"] == "add" || entries == 1)) }' \
        "$file" >"$file.pin" || {
        rm -f "$file.pin"
        fail "${file#"$pin_root"/} has no table $2, or the table holds more than one entry"
    }
    cat "$file.pin" >"$file"
    rm "$file.pin"
}
