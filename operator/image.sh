#!/usr/bin/env bash
#
# Package the operator as a container image, by hand.
#
# A container image is a simple artifact: a tarball of
# tarballs plus three small JSON documents, laid out the way the OCI
# image spec describes. Each *layer* is a tar of a filesystem tree
# (ours has exactly one file in it, the static operator binary); the
# *config* records how to run it and which layers stack into the root
# filesystem; the *manifest* binds config and layers together; and the
# *index* is the entry point that names the manifest. Every blob is
# stored under the SHA-256 of its bytes and referred to only by that
# digest. An image is a small content-addressed database, which is
# why layers dedupe, caches work, and digests can be trusted end to
# end.
#
# Docker would produce this same structure through BuildKit; we write
# it out directly for the same reason image/build.sh drives cpio
# directly: every part of the format stays visible. The result lands
# in the initramfs at /var/lib/rancher/k3s/agent/images/, where k3s
# imports every archive it finds into containerd at startup. That is
# liken's entire image distribution mechanism: no registry, no pull,
# no network. Whoever holds the OS image holds every byte the machine
# will run.

set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
version="$(cat "$here/../VERSION")"

layout="$here/dist/oci"
blobs="$layout/blobs/sha256"
rm -rf "$layout"
mkdir -p "$blobs"

# Store a file as a blob: content-addressing is just "the filename is
# the hash". Prints the digest so callers can reference what they
# stored; the byte sizes the JSON documents need come from stat.
add_blob() {
    local file="$1" digest
    digest="$(sha256sum "$file" | cut -d' ' -f1)"
    mv "$file" "$blobs/$digest"
    echo "$digest"
}

# The layer: one file, /liken-operator, owned by root. The tar flags
# make the archive a pure function of its contents (fixed timestamps,
# numeric ownership, sorted names), so rebuilding an unchanged binary
# yields a byte-identical layer and therefore the same digest.
rootfs="$here/dist/rootfs"
rm -rf "$rootfs"
mkdir -p "$rootfs"
cp "$here/dist/liken-operator" "$rootfs/liken-operator"
tar --create --file "$here/dist/layer.tar" \
    --sort=name --mtime='@0' --owner=0 --group=0 --numeric-owner \
    -C "$rootfs" .
layer_size="$(stat -c%s "$here/dist/layer.tar")"
layer_digest="$(add_blob "$here/dist/layer.tar")"

# The config: the runtime half of the image. diff_ids are digests of
# the *uncompressed* layer tars; ours is stored uncompressed, so the
# same digest appears in both the manifest and here. There is no base
# image and no shell to name; the Entrypoint is the entire runtime
# configuration.
cat >"$here/dist/config.json" <<EOF
{
  "created": "1970-01-01T00:00:00Z",
  "architecture": "amd64",
  "os": "linux",
  "config": {
    "Entrypoint": ["/liken-operator"]
  },
  "rootfs": {
    "type": "layers",
    "diff_ids": ["sha256:$layer_digest"]
  }
}
EOF
config_size="$(stat -c%s "$here/dist/config.json")"
config_digest="$(add_blob "$here/dist/config.json")"

# The manifest: config plus layers, each named by digest and size.
cat >"$here/dist/manifest.json" <<EOF
{
  "schemaVersion": 2,
  "mediaType": "application/vnd.oci.image.manifest.v1+json",
  "config": {
    "mediaType": "application/vnd.oci.image.config.v1+json",
    "digest": "sha256:$config_digest",
    "size": $config_size
  },
  "layers": [
    {
      "mediaType": "application/vnd.oci.image.layer.v1.tar",
      "digest": "sha256:$layer_digest",
      "size": $layer_size
    }
  ]
}
EOF
manifest_size="$(stat -c%s "$here/dist/manifest.json")"
manifest_digest="$(add_blob "$here/dist/manifest.json")"

# The index: the entry point a consumer reads first. The containerd
# annotation is how the image keeps its name through an import: an
# OCI layout has no registry to imply one, so the reference rides
# along inside the archive.
cat >"$layout/index.json" <<EOF
{
  "schemaVersion": 2,
  "manifests": [
    {
      "mediaType": "application/vnd.oci.image.manifest.v1+json",
      "digest": "sha256:$manifest_digest",
      "size": $manifest_size,
      "platform": {"architecture": "amd64", "os": "linux"},
      "annotations": {
        "io.containerd.image.name": "liken.sh/operator:$version",
        "org.opencontainers.image.ref.name": "$version"
      }
    }
  ]
}
EOF

# The layout marker file that declares "this directory is an OCI image
# layout", and then the whole thing becomes one archive.
echo '{"imageLayoutVersion": "1.0.0"}' >"$layout/oci-layout"
tar --create --file "$here/dist/liken-operator-image.tar" \
    --sort=name --mtime='@0' --owner=0 --group=0 --numeric-owner \
    -C "$layout" oci-layout index.json blobs

echo "liken.sh/operator:$version:"
du -sh "$here/dist/liken-operator-image.tar"
