#!/usr/bin/env bash
#
# Keep the finished userland, the dist/ directory that build.sh writes,
# in a registry, so that a build whose inputs did not change takes the
# programs from there and does not run buildroot. A cold buildroot
# build compiles a musl cross-compiler and every program from source,
# and takes most of the time of the OS build job.
#
# Usage:
#   userland/artifact.sh restore <output>...  make dist/ current without
#                                             a build, or fail
#   userland/artifact.sh save                 record the key of the
#                                             dist/ that build.sh just
#                                             wrote, and push it when
#                                             LIKEN_USERLAND_PUSH=1
#
# The Makefile runs `restore`, and runs build.sh and then `save` only
# when `restore` fails. So a failed pull costs a build and never fails
# one.
#
# The key is a hash of every input that changes what build.sh writes:
# each file of this directory that git tracks or would track, and the
# `buildroot` line of toolchains/images.toml, which names the builder
# image. The build reads no other line of that table. git leaves out
# _build/, cache/, and dist/, because .gitignore names them, so the key
# of a tree is the same before and after a build. The key also leaves
# out bluetoothd.config and bluetooth/, which only the bluetoothd image
# builds. buildroot parses the BlueZ recipe in the OS build too, but
# os.config does not select it, so a change to it changes nothing that
# build.sh writes, and the bluetoothd image's build in the same CI run
# fails on a recipe that does not parse. A tree that is not a git
# checkout has no key, and it always builds.
#
# The artifact is ghcr.io/liken-sh/userland:<key>, an OCI artifact
# with one layer, a tarball of dist/. dist/key inside it holds the key
# that the push computed. `restore` checks that key against its own
# and checks that each output the Makefile names is in the tarball, so
# a tag that names the wrong artifact, or a tarball without a program,
# is refused. oras checks each blob that it pulls against the digest
# in the manifest.
#
# The key is a hash of the inputs, not of the bytes of the artifact,
# so nothing that a machine computes from the tree can prove that the
# artifact holds what a build of that tree writes. The trust is in the
# registry: ghcr.io accepts a write to the liken-sh packages only from
# a member of the organization and from the workflow token of this
# repository, and the workflows set LIKEN_USERLAND_PUSH only in the OS
# jobs of a push to refs/heads/main, so main's CI writes every tag. The
# image builds trust ghcr.io the same way when they read their layer
# cache from its :buildcache tags.
#
# LIKEN_USERLAND_REPOSITORY sets another repository, such as a test
# registry on localhost:5000, and LIKEN_USERLAND_PLAIN_HTTP=true lets
# oras talk plain HTTP to it.
#
# oras runs from the `oras` image of toolchains/images.toml, the same
# image that library-operator/appearances/models/push.sh runs, so one
# entry pins oras for both. The container reads the registry
# credentials from ~/.docker/config.json when that file exists. A
# config that sends credentials to a helper program, which the
# container does not have, makes oras fail, so a pull that fails with
# the config is tried once more without it, as an anonymous pull.

set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source-path=SCRIPTDIR source=../../toolchains/pin.sh
source "$here/../../toolchains/pin.sh"

repository="${LIKEN_USERLAND_REPOSITORY:-ghcr.io/liken-sh/userland}"
plain_http="${LIKEN_USERLAND_PLAIN_HTTP:-false}"
out="$here/dist"
config="${DOCKER_CONFIG:-$HOME/.docker}/config.json"

# The pull and the push work in a directory under cache/, which
# .gitignore names, so a directory that an interrupted run leaves
# behind does not change the key.
work=""
trap '[[ -z "$work" ]] || rm -rf "$work"' EXIT

# key: print the key of the tree, or fail when the tree is not a git
# checkout. A file that git tracks and the work tree deleted is not an
# input any more, so the loop skips it.
key() {
    git -C "$here" rev-parse --is-inside-work-tree >/dev/null 2>&1 || return 1
    {
        git -C "$here" ls-files -z --cached --others --exclude-standard \
            -- . ':!bluetoothd.config' ':!bluetooth/' |
            sort -zu |
            while IFS= read -r -d '' file; do
                [[ -f "$here/$file" ]] || continue
                printf '%s  %s\n' "$(sha256sum <"$here/$file" | cut -d' ' -f1)" "$file"
            done
        grep '^buildroot = ' "$pin_root/toolchains/images.toml"
    } | sha256sum | cut -d' ' -f1
}

# run_oras <directory> <use credentials> <argument>...: run oras in
# its pinned image with the directory as its working directory. The
# pull of the artifact is about the size of dist/, so the limit is far
# above a pull on a slow line and far below a build.
run_oras() {
    local dir="$1" credentials="$2" runtime
    shift 2
    runtime="$(container_runtime)"
    [[ -n "$runtime" ]] || fail "needs docker or podman to run oras"
    local args=(run --rm --network host -u "$(id -u):$(id -g)" -e HOME=/tmp
        -v "$dir:/workspace" -w /workspace)
    if [[ "$credentials" == true && -f "$config" ]]; then
        args+=(-e DOCKER_CONFIG=/auth -v "$config:/auth/config.json:ro")
    fi
    timeout 1800 "$runtime" "${args[@]}" "$(image_of oras)" "$@"
}

restore() {
    local key
    [[ $# -gt 0 ]] || fail "restore needs the outputs that the Makefile names"
    key="$(key)" || {
        echo "artifact.sh: this tree is not a git checkout, so the userland has no key" >&2
        return 1
    }

    # make runs build.sh again when any line of toolchains/images.toml
    # changes, and most lines do not change the userland. A dist/ that
    # holds the same key is current, and it only needs new timestamps.
    if [[ "$(cat "$out/key" 2>/dev/null)" == "$key" ]] && (cd "$here" && ls "$@" >/dev/null 2>&1); then
        (cd "$here" && touch "$@")
        echo "the userland in dist/ is current for key ${key:0:12}"
        return 0
    fi

    mkdir -p "$here/cache"
    work="$(mktemp -d "$here/cache/artifact.XXXXXX")"
    echo "pulling the userland from $repository:${key:0:12}..."
    if ! run_oras "$work" true pull --plain-http="$plain_http" "$repository:$key" >&2 &&
        ! { [[ -f "$config" ]] && run_oras "$work" false pull --plain-http="$plain_http" "$repository:$key" >&2; }; then
        echo "artifact.sh: $repository has no userland for key $key that this machine can read" >&2
        return 1
    fi

    mkdir "$work/unpacked"
    tar -xzf "$work/userland.tar.gz" -C "$work/unpacked" || {
        echo "artifact.sh: the pulled userland is not a readable tarball" >&2
        return 1
    }
    local pushed
    pushed="$(cat "$work/unpacked/dist/key" 2>/dev/null || true)"
    if [[ "$pushed" != "$key" ]]; then
        echo "artifact.sh: the artifact at $repository:$key holds the key '$pushed'" >&2
        return 1
    fi
    local file
    for file in "$@"; do
        [[ -f "$work/unpacked/$file" ]] || {
            echo "artifact.sh: the pulled userland holds no $file" >&2
            return 1
        }
    done

    # The tarball keeps the times of the build that pushed it, which
    # are older than a fresh checkout's files, so make would build the
    # userland again on its next run. The key matched, so the files
    # are current.
    find "$work/unpacked/dist" -exec touch {} +
    rm -rf "$out"
    mv "$work/unpacked/dist" "$out"
    echo "pulled the userland for key ${key:0:12}:"
    sed 's/^/  /' "$out/versions"
}

save() {
    local key
    key="$(key)" || {
        echo "artifact.sh: this tree is not a git checkout, so the userland is not saved" >&2
        return 0
    }
    echo "$key" >"$out/key"
    [[ "${LIKEN_USERLAND_PUSH:-}" == 1 ]] || return 0

    mkdir -p "$here/cache"
    work="$(mktemp -d "$here/cache/artifact.XXXXXX")"
    tar -czf "$work/userland.tar.gz" -C "$here" dist

    # A push that fails leaves the next build to compile the userland
    # again. It does not fail this build, which has its programs. The
    # source annotation links a new package on ghcr.io to this
    # repository, so the repository's workflows can write to it.
    if run_oras "$work" true push --plain-http="$plain_http" \
        --artifact-type application/vnd.liken.userland.v1 \
        --annotation org.opencontainers.image.source=https://github.com/liken-sh/liken \
        --annotation org.opencontainers.image.description="The static programs of the liken OS, built by liken/userland/build.sh" \
        "$repository:$key" \
        userland.tar.gz:application/vnd.liken.userland.dist.v1.tar+gzip; then
        echo "pushed the userland as $repository:$key"
    else
        echo "artifact.sh: the push to $repository failed; the next build compiles the userland again" >&2
    fi
}

case "${1:-}" in
restore)
    shift
    restore "$@"
    ;;
save) save ;;
*) fail "usage: artifact.sh restore <output>... | artifact.sh save" ;;
esac
