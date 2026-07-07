#!/usr/bin/env bash
#
# Publish a built release: lay out dist/<version>/ the way the release
# server serves it, and print the catalog entry that names it.
#
# The artifacts come straight from the install image's payload
# (build/<version>/image/install-root/usr/share/liken/release), and
# that is deliberate: install.sh already generated a release.yaml from
# the exact bytes it packed, so publishing the same files under the
# same document means the installer's copy and the server's copy are
# one document, byte for byte. A machine installed from the "USB
# stick" and a machine that downloaded the release verified the very
# same promises.
#
# The last thing printed is the catalog entry: the release document's
# own sha256, which is what goes into the Cluster's
# spec.releases.catalog. That digest is the root of the trust chain —
# the API names the document, the document names the artifacts — and
# it exists only here, at publish time, because the artifacts embed
# this checkout's identity (the cluster CA, the join token) and so no
# digest is stable enough to commit to git.
set -euo pipefail

version="$1"
here="$(cd "$(dirname "$0")" && pwd)"

payload="$here/build/$version/image/install-root/usr/share/liken/release"
out="$here/dist/$version"

rm -rf "$out"
mkdir -p "$out"
cp "$payload/vmlinuz" "$payload/liken.cpio" "$payload/release.yaml" "$out/"

# install.cpio is published for people making fresh machines, but the
# release document doesn't name it: an upgrading machine never fetches
# it, and the stick carries its own embedded copy of the document.
cp "$here/build/$version/image/install.cpio" "$out/"

digest="$(sha256sum "$out/release.yaml" | cut -d' ' -f1)"

echo
echo "published liken $version to releases/dist/$version:"
du -sh "$out"/*
echo
echo "catalog entry for the Cluster's spec.releases.catalog:"
echo "  - version: $version"
echo "    digest: sha256:$digest"
