#!/usr/bin/env bash
#
# Package the log relays as a container image, by hand.
#
# This is the operator's recipe applied to a second binary;
# operator/image.sh walks through what each piece of an OCI layout is
# and why writing one is just hashing three JSON documents and a tar.
# The short version: the layer is a tar holding exactly one file (the
# static liken-logs binary), the config names it as the Entrypoint,
# the manifest binds them, and the index gives the image its names.
#
# The image carries one binary but backs all four relay containers
# of the machine-logs DaemonSet: each passes a different verb
# (kernel, liken, k3s, containerd) as its args, and the Entrypoint
# stays the same. Like the operator's image, it lands in the
# initramfs where k3s auto-imports it: no registry, no pull, and the
# "installed" tag always resolves, on every node, to the build that
# node's own OS carried in.

set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

version="${LIKEN_VERSION:-$(cat "$here/../VERSION")}"
dist="${DIST:-$here/dist}"

layout="$dist/oci"
blobs="$layout/blobs/sha256"
rm -rf "$layout"
mkdir -p "$blobs"

add_blob() {
    local file="$1" digest
    digest="$(sha256sum "$file" | cut -d' ' -f1)"
    mv "$file" "$blobs/$digest"
    echo "$digest"
}

# The layer: one file, /liken-logs, owned by root, with the tar made
# a pure function of its contents so unchanged builds keep their
# digests.
rootfs="$dist/rootfs"
rm -rf "$rootfs"
mkdir -p "$rootfs"
cp "$dist/liken-logs" "$rootfs/liken-logs"
tar --create --file "$dist/layer.tar" \
    --sort=name --mtime='@0' --owner=0 --group=0 --numeric-owner \
    -C "$rootfs" .
layer_size="$(stat -c%s "$dist/layer.tar")"
layer_digest="$(add_blob "$dist/layer.tar")"

cat >"$dist/config.json" <<EOF
{
  "created": "1970-01-01T00:00:00Z",
  "architecture": "amd64",
  "os": "linux",
  "config": {
    "Entrypoint": ["/liken-logs"]
  },
  "rootfs": {
    "type": "layers",
    "diff_ids": ["sha256:$layer_digest"]
  }
}
EOF
config_size="$(stat -c%s "$dist/config.json")"
config_digest="$(add_blob "$dist/config.json")"

cat >"$dist/manifest.json" <<EOF
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
manifest_size="$(stat -c%s "$dist/manifest.json")"
manifest_digest="$(add_blob "$dist/manifest.json")"

# Two names for one image, exactly like the operator's: the versioned
# tag says what this build is, and the stable "installed" tag is the
# one the machine-logs DaemonSet pins, so its pod spec never changes
# across releases while each node runs its own OS's build.
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
        "io.containerd.image.name": "liken.sh/logs:$version",
        "org.opencontainers.image.ref.name": "$version"
      }
    },
    {
      "mediaType": "application/vnd.oci.image.manifest.v1+json",
      "digest": "sha256:$manifest_digest",
      "size": $manifest_size,
      "platform": {"architecture": "amd64", "os": "linux"},
      "annotations": {
        "io.containerd.image.name": "liken.sh/logs:installed",
        "org.opencontainers.image.ref.name": "installed"
      }
    }
  ]
}
EOF

echo '{"imageLayoutVersion": "1.0.0"}' >"$layout/oci-layout"
tar --create --file "$dist/liken-logs-image.tar" \
    --sort=name --mtime='@0' --owner=0 --group=0 --numeric-owner \
    -C "$layout" oci-layout index.json blobs

echo "liken.sh/logs:$version:"
du -sh "$dist/liken-logs-image.tar"
