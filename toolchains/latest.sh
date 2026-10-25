#!/usr/bin/env bash
#
# Report what the toolchains component pins, and what each upstream
# offers now.
#
# images.toml names each outside image that the repository builds
# from or runs, once, by tag and, for a build image, by digest. The
# Go toolchain is the `go` line that every go.mod names, and the Rust
# toolchain is the `channel` of every rust-toolchain.toml. The Go
# modules and the Rust crates are the direct and tool requirements of
# every go.mod and the exact `=` pins of every Cargo.toml. The CI
# checks keep the copies of each pin equal, and this report compares
# each pin with its upstream.
#
# The bytes come from the registries: Docker Hub, ghcr.io, gcr.io,
# and registry.k8s.io, each read through the registry API with an
# anonymous token, so the report needs no docker daemon. The Go and
# Rust versions come from go.dev and static.rust-lang.org, the
# modules from the Go module proxy, and the crates from crates.io.
#
# Root plan 83 gives the rule: the newest release, from the newest
# channel that the registry offers. The channel of `golang` and `rust`
# is the Debian release in the tag, and the newest channel is the
# newest Debian release that the image publishes, because Debian's
# own newest release can be ahead of the image. The channel of
# `alpine` is its release, and the newest is `edge`. `ubuntu` is the
# base of `indi`, which installs the INDI PPA, so its newest channel
# is the newest Ubuntu series that the PPA publishes. The k8s.io
# modules follow `liken/k3s/VERSION`, because the client libraries
# must speak the API server's version.
#
# A build image also has a `digest` row: the digest of the pinned tag
# today. A registry rebuilds a tag for a fix in its packages, so a
# moved digest is a new build of the same release.
#
# Usage:
#   toolchains/latest.sh                      report every pin
#   toolchains/latest.sh --bump <key>         move one images.toml
#                                             entry to the newest tag
#   toolchains/latest.sh --bump go            move the golang image
#                                             and every go.mod
#   toolchains/latest.sh --bump rustc         move the rust image and
#                                             every rust-toolchain.toml
#   toolchains/latest.sh --bump go:<module>   move one Go module in
#                                             every go.mod
#   toolchains/latest.sh --bump crate:<name>  move one crate in every
#                                             Cargo.toml and Cargo.lock

set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
root="$(cd "$here/.." && pwd)"
images="$here/images.toml"

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

row() { printf '%s\t%s\t%s\t%s\t%s\t%s\t%s\n' "$@"; }

# The digest is 71 characters and the table has to stay readable, so
# the rows carry enough of it to tell two apart.
short() { if [[ -n "$1" ]]; then echo "${1#sha256:}" | cut -c1-12; else echo '?'; fi; }

# manifests <regex> lists the files in the tree whose name matches.
# git's list leaves out what .gitignore names, such as caches and
# build output, and the filter leaves out test fixtures, which hold
# copies that no build reads.
manifests() {
    git -C "$root" ls-files --cached --others --exclude-standard |
        grep -E "(^|/)$1\$" | grep -v '/testdata/' | sort -u |
        while read -r file; do
            if [[ -f "$root/$file" ]]; then echo "$root/$file"; fi
        done
}

# --- images.toml ------------------------------------------------------

# entries prints "section key reference" for every line of images.toml.
entries() {
    awk -F' = ' '/^\[/ { gsub(/[][]/, ""); s = $0; next }
        /^[a-z]/ { gsub(/"/, "", $2); print s, $1, $2 }' "$images"
}
ref_of() { entries | awk -v k="$1" '$2 == k { print $3 }'; }
section_of() { entries | awk -v k="$1" '$2 == k { print $1 }'; }

# A reference is repository:tag@digest. The repository can carry a
# host, and a bare name is an official image on Docker Hub.
repo_of() { local r="${1%@*}"; echo "${r%:*}"; }
tag_of() { local r="${1%@*}"; echo "${r##*:}"; }
digest_of() { [[ "$1" == *@* ]] && echo "${1#*@}" || true; }

# registry <repository> <path> [curl args]: one request to the
# repository's registry. A registry that answers 401 names the token
# service in WWW-Authenticate, and an anonymous pull token from there
# reads any public image.
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
    auth="$(curl -sS -o /dev/null -D - --max-time 20 "$url" | tr -d '\r' |
        grep -i '^www-authenticate:' || true)"
    if [[ -z "$auth" ]]; then
        curl -fsSL --retry 3 --max-time 60 "$@" "$url"
        return
    fi
    realm="$(sed -n 's/.*realm="\([^"]*\)".*/\1/p' <<<"$auth")"
    service="$(sed -n 's/.*service="\([^"]*\)".*/\1/p' <<<"$auth")"
    token="$(curl -fsS --retry 3 --max-time 20 \
        "$realm?service=$service&scope=repository:$name:pull" | jq -r '.token // .access_token')"
    curl -fsSL --retry 3 --max-time 60 -H "Authorization: Bearer $token" "$@" "$url"
}

# The digest that a tag names today. The Accept header lists the
# index types first, so a multi-platform tag answers with the digest
# of its index, which is what a pin names.
registry_digest() {
    registry "$1" "manifests/$2" -I \
        -H 'Accept: application/vnd.oci.image.index.v1+json, application/vnd.docker.distribution.manifest.list.v2+json, application/vnd.oci.image.manifest.v1+json, application/vnd.docker.distribution.manifest.v2+json' |
        tr -d '\r' | awk 'tolower($1) == "docker-content-digest:" { print $2 }'
}

# The tag list of a repository, read once and kept for the run.
tags() {
    local file
    file="$work/tags-$(echo "$1" | tr '/:' '__')"
    [[ -s "$file" ]] || registry "$1" 'tags/list?n=100000' | jq -r '.tags[]' >"$file"
    cat "$file"
}

newest_matching() { grep -E "$1" | sort -V | tail -1; }

# The Debian releases in order. distro-info-data is Debian's own list,
# and it includes the releases that are not out yet.
debian_codenames() {
    curl -fsS --retry 3 --max-time 20 \
        https://salsa.debian.org/debian/distro-info-data/-/raw/main/debian.csv |
        awk -F, '$1 ~ /^[0-9]+$/ { print $3 }'
}

# The newest Debian release that an image publishes, from its
# major-version alias tags such as 1-trixie.
newest_variant() {
    local repo="$1" codenames
    codenames="$(debian_codenames)" || return 1
    tags "$repo" >"$work/variant-tags"
    tac <<<"$codenames" | while read -r codename; do
        if grep -qx "1-$codename" "$work/variant-tags"; then
            echo "$codename"
            break
        fi
    done
}

newest_go() { curl -fsS --retry 3 --max-time 20 'https://go.dev/dl/?mode=json' | jq -r '.[0].version' | sed 's/^go//'; }
newest_rust() {
    curl -fsS --retry 3 --max-time 20 https://static.rust-lang.org/dist/channel-rust-stable.toml |
        awk '/^\[pkg\.rust\]/ { getline; print $3 }' | tr -d '"'
}

# The Ubuntu series by version, from Launchpad.
ubuntu_series() {
    curl -fsS --retry 3 --max-time 20 https://api.launchpad.net/devel/ubuntu/series |
        jq -r '.entries[] | "\(.version) \(.name)"' | sort -V
}

# The newest Ubuntu series that the INDI PPA publishes, as
# "version name". indi installs from that PPA, so the base image can
# move no further than the PPA does.
ppa_newest_series() {
    local version name
    ubuntu_series | tac | while read -r version name; do
        if curl -fs -o /dev/null --max-time 20 \
            "https://ppa.launchpadcontent.net/mutlaqja/ppa/ubuntu/dists/$name/Release"; then
            echo "$version $name"
            break
        fi
    done
}

# The GitHub API allows 60 anonymous requests an hour, so the report
# sends a token when one is at hand.
github_auth=()
token="${GITHUB_TOKEN:-$(gh auth token 2>/dev/null || true)}"
[[ -n "$token" ]] && github_auth=(-H "Authorization: Bearer $token")

# latest_image <key> sets four variables for one entry: the newest tag
# under the rule, the channel the pin reads, the newest channel, and
# a note. An empty latest_tag means the upstream did not answer.
latest_image() {
    local key="$1" ref repo tag version variant newest series
    ref="$(ref_of "$key")"
    repo="$(repo_of "$ref")"
    tag="$(tag_of "$ref")"
    latest_tag="" channel=- newest_channel=- note=""
    case "$key" in
    golang | rust)
        channel="${tag#*-}"
        if [[ "$key" == golang ]]; then
            version="$(newest_go)" || version=""
        else
            version="$(newest_rust)" || version=""
        fi
        variant="$(newest_variant "$repo")" || variant=""
        newest_channel="${variant:-?}"
        [[ -n "$version" && -n "$variant" ]] && latest_tag="$version-$variant"
        note="the newest $([[ "$key" == golang ]] && echo Go || echo Rust) release, in the newest Debian that the image publishes"
        ;;
    alpine)
        channel="$tag"
        newest_channel=edge
        newest="$(tags alpine | newest_matching '^[0-9]+\.[0-9]+\.[0-9]+$')" || newest=""
        [[ -n "$newest" ]] && latest_tag=edge
        note="the static builder; the newest release is ${newest:-?}"
        ;;
    ubuntu)
        series="$(ubuntu_series)" || series=""
        channel="$(awk -v v="$tag" '$1 == v { print $2 }' <<<"$series")"
        read -r version newest < <(ppa_newest_series || true) || true
        newest_channel="${newest:-?}"
        latest_tag="${version:-}"
        note="indi's base; mutlaqja/ppa publishes no newer series"
        [[ "$version" == "$(tail -1 <<<"$series" | cut -d' ' -f1)" ]] ||
            note="indi's base; Ubuntu's newest series is $(tail -1 <<<"$series" | cut -d' ' -f2), and mutlaqja/ppa does not publish it"
        ;;
    oras)
        version="$(curl -fsS --retry 3 --max-time 20 "${github_auth[@]}" \
            https://api.github.com/repos/oras-project/oras/releases/latest | jq -r .tag_name)" || version=""
        latest_tag="$version"
        note="the newest release of oras-project/oras"
        ;;
    corrosion)
        latest_tag="$(tags "$repo" | newest_matching '^[0-9]{4}\.[0-9]{2}\.[0-9]{2}-[0-9]+$')" || latest_tag=""
        note="the newest release of liken-sh/corrosion"
        ;;
    debian)
        channel=sid newest_channel=sid latest_tag="$tag"
        note="vulkan/latest.sh moves it with the snapshot date"
        ;;
    distroless)
        latest_tag="$tag"
        note="static follows the newest Debian that distroless publishes"
        ;;
    eclipse-mosquitto)
        # Mosquitto publishes 2.1 and later only with an -alpine tag,
        # and its `latest` tag names that image, so the -alpine tag is
        # the newest release and not another channel. The plain tags
        # end at 2.0.
        version="$(tags "$repo" | sed 's/-alpine$//' | newest_matching '^[0-9]+\.[0-9]+\.[0-9]+$')" || version=""
        if [[ -n "$version" ]] && tags "$repo" | grep -qx "$version"; then
            latest_tag="$version"
        elif [[ -n "$version" ]]; then
            latest_tag="$version-alpine"
            note="$version is published only as $version-alpine, which the latest tag names"
        fi
        ;;
    *)
        # The CSI sidecars tag v1.2.3, and curl and busybox tag 1.2.3.
        latest_tag="$(tags "$repo" | newest_matching '^v?[0-9]+\.[0-9]+\.[0-9]+$')" || latest_tag=""
        ;;
    esac
}

# report_image prints the rows of one entry. debian and distroless
# follow a moving tag, so their one row compares digests.
report_image() {
    local key="$1" ref repo tag pinned_digest now
    ref="$(ref_of "$key")"
    repo="$(repo_of "$ref")"
    tag="$(tag_of "$ref")"
    pinned_digest="$(digest_of "$ref")"
    latest_image "$key"
    case "$key" in
    debian | distroless)
        now="$(registry_digest "$repo" "$tag")" || now=""
        row "$key" "$(short "$pinned_digest")" "$(short "$now")" \
            "$channel" "$newest_channel" - "$tag: $note"
        return
        ;;
    esac
    row "$key" "$tag" "${latest_tag:-?}" "$channel" "$newest_channel" - "${note:-the newest release}"
    if [[ -n "$pinned_digest" ]]; then
        now="$(registry_digest "$repo" "$tag")" || now=""
        row '  digest' "$(short "$pinned_digest")" "$(short "$now")" - - - \
            "the digest of $tag today"
    fi
}

# --- Go ----------------------------------------------------------------

go_mods() { manifests 'go\.mod'; }

# The go line of every go.mod, each distinct value once.
go_lines() { go_mods | xargs -r cat | awk '$1 == "go" { print $2 }' | sort -uV | paste -sd,; }

# go_requires prints "module version file" for each direct and tool
# requirement. go.mod marks a tool's module `// indirect` when no
# package imports it, so a requirement that a tool line names counts
# as direct. A requirement that a replace points at a directory is a
# module of this repository and has no upstream.
go_requires() {
    local mod
    while read -r mod; do
        go mod edit -json "$mod" | jq -r --arg f "$mod" '
            ([.Replace[]? | select(.New.Version == null) | .Old.Path]) as $local |
            (.Tool // [] | map(.Path)) as $tools |
            .Require[]? |
            select(.Path as $p | ($local | index($p)) == null) |
            select((.Indirect | not) or (.Path as $p | any($tools[]; . == $p or startswith($p + "/")))) |
            "\(.Path) \(.Version) \($f)"'
    done < <(go_mods)
}

# The k8s.io modules that k3s tags with the Kubernetes minor version,
# as v0.<minor>.<patch>, follow the k3s pin.
k8s_version() { sed -E 's/^v1\.([0-9]+)\.([0-9]+).*/v0.\1.\2/' "$root/liken/k3s/VERSION"; }
follows_k3s() { [[ "$1" == k8s.io/* && "$2" =~ ^v0\.[1-9][0-9]+\.[0-9]+$ ]]; }

# latest_modules reads every requirement once and asks the module
# proxy for each module's newest release in one go list call, which
# also skips retracted versions.
latest_modules() {
    go_requires >"$work/requires"
    local modules
    modules="$(awk '{ print $1 "@latest" }' "$work/requires" | sort -u)"
    # shellcheck disable=SC2086 # one argument per module
    (cd "$work" && GOWORK=off GOFLAGS='' go list -m -e -json $modules) |
        jq -r '"\(.Path) \(.Version // "?")"' >"$work/latest-modules"
}

report_modules() {
    local k8s path versions latest
    k8s="$(k8s_version)"
    latest_modules
    while read -r path; do
        versions="$(awk -v p="$path" '$1 == p { print $2 }' "$work/requires" | sort -uV | paste -sd,)"
        if follows_k3s "$path" "${versions%%,*}"; then
            [[ "$versions" == "$k8s" ]] ||
                row "  go:$path" "$versions" "$k8s" - - - "follows liken/k3s"
            continue
        fi
        latest="$(awk -v p="$path" '$1 == p { print $2 }' "$work/latest-modules")"
        [[ "$versions" == "$latest" ]] || if [[ "$latest" =~ -[0-9]{14}-[0-9a-f]{12}$ ]]; then
            row "  go:$path" "$versions" "$latest" - - - "the newest commit; the module has no release"
        else
            row "  go:$path" "$versions" "${latest:-?}" - - - "the newest release on the module proxy"
        fi
    done < <(awk '{ print $1 }' "$work/requires" | sort -u)
}

# --- Rust --------------------------------------------------------------

toolchain_lines() {
    manifests 'rust-toolchain\.toml' | xargs -r sed -n 's/^channel = "\(.*\)"$/\1/p' | sort -uV | paste -sd,
}

# crate_pins prints "crate version" for each exact pin, in either form:
# name = "=1.2.3" or name = { version = "=1.2.3", ... }.
crate_pins() {
    manifests 'Cargo\.toml' | xargs -r grep -hE '^[A-Za-z0-9_-]+ *= *(\{[^}]*version *= *)?"=[^"]+"' |
        sed -E 's/^([A-Za-z0-9_-]+) *=.*"=([^"]+)".*/\1 \2/' | sort -u
}

# crates.io asks each client to name itself, and to send about one
# request each second. The crates are deduplicated first, so the
# report sends one request for each crate.
newest_crate() {
    sleep 1
    curl -fsS --retry 3 --max-time 20 -A 'liken toolchains/latest.sh (https://github.com/liken-sh/liken)' \
        "https://crates.io/api/v1/crates/$1" | jq -r '.crate.max_stable_version'
}

report_crates() {
    local name versions latest
    crate_pins >"$work/crates"
    while read -r name; do
        versions="$(awk -v n="$name" '$1 == n { print $2 }' "$work/crates" | sort -uV | paste -sd,)"
        latest="$(newest_crate "$name")" || latest=""
        [[ "$versions" == "$latest" ]] ||
            row "  crate:$name" "$versions" "${latest:-?}" - - - "the newest stable release on crates.io"
    done < <(awk '{ print $1 }' "$work/crates" | sort -u)
}

# --- the report ----------------------------------------------------------

if [[ "${1:-}" != "--bump" ]]; then
    while read -r _ key _; do
        report_image "$key"
    done < <(entries)
    go_latest="$(newest_go)" || go_latest=""
    row go "$(go_lines)" "${go_latest:-?}" - - - "the go line of every go.mod"
    report_modules
    rust_latest="$(newest_rust)" || rust_latest=""
    row rustc "$(toolchain_lines)" "${rust_latest:-?}" - - - "the channel of every rust-toolchain.toml"
    report_crates
    exit 0
fi

# --- the bumps -----------------------------------------------------------

target="${2:-}"
[[ -n "$target" ]] || {
    echo "latest.sh: --bump needs a name: an images.toml key, go, rustc, go:<module>, or crate:<name>" >&2
    exit 1
}

# The pinned components whose Dockerfile names an image bare, as in
# `FROM golang` or `--from=golang`. CI hashes the resolved reference
# into each one's recipe, so each needs a new revision.
pinned_users() {
    local dockerfile dir
    manifests 'Dockerfile[^/]*' | while read -r dockerfile; do
        grep -qE "^FROM +$1( |$)|--from=$1( |$)" "$dockerfile" || continue
        dir="$(dirname "$dockerfile")"
        while [[ "$dir" != "$root" && ! -f "$dir/package.toml" ]]; do dir="$(dirname "$dir")"; done
        grep -q '^revision = ' "$dir/package.toml" 2>/dev/null && echo "${dir#"$root"/}"
    done | sort -u | paste -sd' '
}

# bump_image writes the newest tag of one entry, with its digest for a
# build image, and prints what moved.
bump_image() {
    local key="$1" ref repo tag digest new
    ref="$(ref_of "$key")"
    [[ -n "$ref" ]] || {
        echo "latest.sh: images.toml has no entry named $key" >&2
        exit 1
    }
    [[ "$key" != debian ]] || {
        echo "latest.sh: the debian digest moves with the snapshot date; run vulkan/latest.sh --bump" >&2
        exit 1
    }
    repo="$(repo_of "$ref")"
    tag="$(tag_of "$ref")"
    latest_image "$key"
    [[ -n "$latest_tag" ]] || {
        echo "latest.sh: the upstream of $key did not answer" >&2
        exit 1
    }
    new="$repo:$latest_tag"
    if [[ "$(section_of "$key")" == build ]]; then
        digest="$(registry_digest "$repo" "$latest_tag")" || digest=""
        [[ "$digest" =~ ^sha256:[0-9a-f]{64}$ ]] || {
            echo "latest.sh: $new has no digest; the registry may not publish it yet" >&2
            exit 1
        }
        new="$new@$digest"
    fi
    if [[ "$new" == "$ref" ]]; then
        echo "$key $tag is current"
        return
    fi
    sed -i "s|^$key = \".*\"$|$key = \"$new\"|" "$images"
    echo "$key: $ref -> $new"
    if [[ "$(section_of "$key")" == build ]]; then
        users="$(pinned_users "$key")"
        echo "next: make workflows at the top of the repository, and a new revision of: ${users:-no pinned component}"
    else
        echo "next: make each manifest, guide, and skill name $new:"
        git -C "$root" grep --untracked -lF "$ref" | sed 's/^/  /' || true
    fi
}

case "$target" in
go | golang)
    bump_image golang
    version="$(tag_of "$(ref_of golang)")"
    version="${version%%-*}"
    go_mods | while read -r mod; do go mod edit -go="$version" "$mod"; done
    echo "go: every go.mod names go $version"
    ;;
rustc | rust)
    bump_image rust
    version="$(tag_of "$(ref_of rust)")"
    version="${version%%-*}"
    manifests 'rust-toolchain\.toml' | xargs -r sed -i "s/^channel = \".*\"$/channel = \"$version\"/"
    echo "rustc: every rust-toolchain.toml names $version"
    ;;
go:*)
    module="${target#go:}"
    go_requires >"$work/requires"
    grep -q "^$module " "$work/requires" || {
        echo "latest.sh: no go.mod requires $module directly" >&2
        exit 1
    }
    # The k8s.io modules that follow k3s require each other at the
    # same version, so they move together.
    if follows_k3s "$module" "$(awk -v p="$module" '$1 == p { print $2; exit }' "$work/requires")"; then
        version="$(k8s_version)"
        awk '{ print $1, $2 }' "$work/requires" | sort -u | while read -r path pinned; do
            if follows_k3s "$path" "$pinned"; then echo "$path"; fi
        done >"$work/group"
    else
        version="$(cd "$work" && GOWORK=off GOFLAGS='' go list -m -f '{{.Version}}' "$module@latest")"
        echo "$module" >"$work/group"
    fi
    # go get writes go.mod and go.sum together, and raises any other
    # requirement that the new version needs.
    awk 'NR == FNR { group[$1] = 1; next } $1 in group { print $3 }' \
        "$work/group" "$work/requires" | sort -u | while read -r mod; do
        mapfile -t mine < <(awk -v f="$mod" -v v="$version" 'NR == FNR { group[$1] = 1; next }
            $3 == f && $1 in group { print $1 "@" v }' "$work/group" "$work/requires" | sort -u)
        (cd "$(dirname "$mod")" && GOWORK=off go get "${mine[@]}")
        echo "${mod#"$root"/}: ${mine[*]}"
    done
    echo "next: make test in each component above"
    ;;
crate:*)
    crate="${target#crate:}"
    crate_pins >"$work/crates"
    old="$(awk -v n="$crate" '$1 == n { print $2 }' "$work/crates" | sort -V | paste -sd' ')"
    [[ -n "$old" ]] || {
        echo "latest.sh: no Cargo.toml pins $crate exactly" >&2
        exit 1
    }
    version="$(newest_crate "$crate")"
    [[ -n "$version" && "$version" != null ]] || {
        echo "latest.sh: crates.io did not answer for $crate" >&2
        exit 1
    }
    manifests 'Cargo\.toml' | xargs -r sed -i -E "/^$crate *=/ s/\"=[^\"]+\"/\"=$version\"/"
    # Each lock file records the pinned version too, and cargo moves
    # it with the one crate and nothing else.
    manifests 'Cargo\.lock' | while read -r lock; do
        for v in $old; do
            grep -A1 "^name = \"$crate\"$" "$lock" | grep -qx "version = \"$v\"" || continue
            (cd "$(dirname "$lock")" && cargo update --quiet -p "$crate@$v" --precise "$version")
            echo "${lock#"$root"/}: $crate $v -> $version"
        done
    done
    echo "crate $crate: $old -> $version in every Cargo.toml"
    echo "next: make test in each component that builds the crate"
    ;;
*)
    bump_image "$target"
    ;;
esac
