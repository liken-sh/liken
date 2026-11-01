#!/usr/bin/env bash
#
# Install cargo-auditable, the cargo subcommand that every Rust build of
# an image runs as `cargo auditable build`.
#
# A Rust binary holds no record of the crates it was compiled from, so
# syft and Trivy list none of them from an image, and a scan matches no
# advisory against them. cargo-auditable records the crate graph of the
# build, compressed, in the binary's `.dep-v0` section, and syft and
# Trivy read that section. So every Dockerfile that builds a Rust
# binary installs it with this script and builds with it.
#
# The pin is the version and the digest below, and latest.sh beside
# this file reports and moves both. Each Dockerfile takes this
# directory's parent as the build context named toolchains, copies this
# script and toolchains/pin.sh, and runs the script, so one pin serves
# every image.
#
# The archive is the upstream project's own build for
# x86_64-unknown-linux-gnu, the target of the rust image in
# toolchains/images.toml.
#
# Usage:
#   cargo-auditable/fetch.sh <dir>    install the binary into <dir>
#
# The archive is cached in cargo-auditable/cache/<version>/.

set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source-path=SCRIPTDIR source=../pin.sh
source "$here/../pin.sh"

require_tools tar xz sha256sum

version="0.7.7"
sha256="4c8d3b08c3c34e9f2333367224dbbfb4aab68067698b43de59fe19aded3416dd"

(($# == 1)) || fail "usage: fetch.sh <dir>"
out="$1"

asset="cargo-auditable-x86_64-unknown-linux-gnu"
archive="$here/cache/$version/$asset.tar.xz"
fetch_verified "https://github.com/rust-secure-code/cargo-auditable/releases/download/v$version/$asset.tar.xz" \
    "$sha256" "$archive"

mkdir -p "$out"
tar -xJf "$archive" -C "$out" --strip-components=1 "$asset/cargo-auditable"
echo "cargo-auditable $version in $out"
