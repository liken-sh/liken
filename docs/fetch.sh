#!/usr/bin/env bash
#
# Vendor Hugo: the static site generator that builds the manual.
#
# Hugo turns the Markdown in content/ into the HTML tree that nginx
# serves, ahead of time, on the machine that runs this build. Nothing
# renders on a server and nothing renders in a browser: the site is
# files. Hugo is a build tool in the same sense a compiler is, so a
# release never redistributes its bytes, and the licensing domain
# carries no entry for it.
#
# The pin is a Hugo release version (docs/VERSION), and this fetch
# takes the standard linux-amd64 build, not the extended one: the
# site's stylesheet is plain CSS, so the extended build's SCSS
# support would be dead weight. Hugo publishes a checksums file with
# each release, and this digest comes from it. A version bump must
# update VERSION and the digest together, and the fetch fails with
# an error when they disagree.
#
# A version bump can also change Hugo's template lookup rules, which
# the layouts/ tree depends on. Build the site after a bump before
# trusting it.
#
# Usage:
#   docs/fetch.sh              fetch the version pinned in docs/VERSION
#
# The binary lands in docs/dist/hugo/<version>/hugo, cached in
# docs/cache/.

set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

for tool in curl sha256sum tar; do
    command -v "$tool" >/dev/null || {
        echo "fetch.sh: missing required tool: $tool" >&2
        exit 1
    }
done

version="$(cat "$here/VERSION")"
tarball="hugo_${version}_linux-amd64.tar.gz"
url="https://github.com/gohugoio/hugo/releases/download/v$version/$tarball"

digest="d9c8b17285ea4ec004d9f814273ea910f2051ce02c284993fd1f91ba455ae50d"

cache="$here/cache/$version"
out="$here/dist/hugo/$version"
mkdir -p "$cache"

if ! sha256sum --check --status <<<"$digest  $cache/$tarball" >/dev/null 2>&1; then
    echo "downloading hugo $version"
    curl -fL --progress-bar -o "$cache/$tarball" "$url"
    sha256sum --check --quiet <<<"$digest  $cache/$tarball"
fi

# The tarball carries the binary plus its license and readme. Only
# the binary is extracted: the license travels with the tarball in
# cache/, and nothing here is redistributed. The touch matters: tar
# preserves the archive's timestamps, and Make would read the old
# date as "stale against fetch.sh" and refetch on every build.
rm -rf "$out"
mkdir -p "$out"
tar -xzf "$cache/$tarball" -C "$out" hugo
touch "$out/hugo"

echo
echo "hugo $version:"
"$out/hugo" version
