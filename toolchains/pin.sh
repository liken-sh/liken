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
# The digest is the one that the calling script pins, often from a
# `declare -A` table that holds it beside the version it belongs to,
# as in "${k3s_sha256[$version]:-}". latest.sh --bump writes it, so a
# fetch never reads a digest from upstream. An empty digest means that
# the script pins none for this version, and the fetch stops.
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
    if ((status == 0)) && [[ -n "$signer" ]]; then
        awk -v f="$signer" '$2 == "VALIDSIG" && $NF == f { found = 1 } END { exit !found }' \
            "$work/status" || status=2
    fi
    [[ -n "$file" || "$status" != 0 ]] || cat "$work/signed"
    rm -rf "$work"
    case "$status" in
    1) fail "$signature does not verify against ${key#"$pin_root"/}" ;;
    2) fail "$signature is not signed by $signer" ;;
    esac
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

# repin_sources: bring the digests of liken/licensing/sources.sh up to
# the bytes that its URLs serve after a bump. A bump of an OS domain
# whose version the source mirror names calls it, because the mirror
# must serve the source of the version that a release ships. A refused
# re-pin leaves the domain pin moved and the mirror behind it, which
# is a release that cannot serve its own sources, so the message says
# that plainly: the fix is a hand edit.
repin_sources() {
    "$pin_root/liken/licensing/sources.sh" --repin ||
        fail "the pin moved; licensing/sources.sh still needs a hand edit"
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
