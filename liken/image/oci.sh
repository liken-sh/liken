#!/usr/bin/env bash
#
# This script packages one static binary as a container image, by
# hand.
#
# A container image is a simple artifact: a tarball of tarballs, plus
# three small JSON documents, laid out the way the OCI image spec
# describes. Each layer is a tar of a filesystem tree (ours has
# exactly one file in it, the static binary). The config records how
# to run the image and which layers stack into the root filesystem.
# The manifest binds the config and the layers together. The index is
# the entry point that names the manifest. The image stores every
# blob under the SHA-256 digest of its bytes, and refers to it only
# by that digest. An image is therefore a small content-addressed
# database. This is why layers dedupe, why caches work, and why a
# program can trust a digest end to end.
#
# Docker would produce this same structure through BuildKit. This
# script writes the structure out directly, for the same reason
# image/build.sh drives cpio directly: every part of the format stays
# visible. The result lands in the initramfs at
# /var/lib/rancher/k3s/agent/images/, where k3s imports every archive
# it finds into containerd at startup. That is liken's entire image
# distribution mechanism: no registry, no pull, no network. The OS
# image therefore carries every byte the machine will run.
#
# The recipe takes one static binary in and produces one image
# tarball out, so it lives here in the image domain, and each
# consumer's Makefile calls it. Four images ship this way: the
# machine operator, the cluster operator, the log relays, and the
# iscsi feature's iscsid (a vendored binary, not a liken program, but
# packaged the same way):
#
#   oci.sh <binary> <image>     e.g. oci.sh liken-machine-operator liken.sh/machine-operator
#
# DIST names the directory that holds <binary> and receives
# <binary>-image.tar. LIKEN_VERSION is the version to stamp into the
# image's name.

set -euo pipefail

binary="${1:?usage: oci.sh <binary> <image>}"
image="${2:?usage: oci.sh <binary> <image>}"
version="${LIKEN_VERSION:?LIKEN_VERSION must be set; the Makefiles pass it via version.mk}"
dist="${DIST:?DIST must name the directory holding $binary}"

layout="$dist/oci"
blobs="$layout/blobs/sha256"
rm -rf "$layout"
mkdir -p "$blobs"

# add_blob stores a file as a blob: content-addressing simply means
# "the filename is the hash". It prints the digest so callers can
# reference what they stored. The byte sizes the JSON documents need
# come from stat.
add_blob() {
    local file="$1" digest
    digest="$(sha256sum "$file" | cut -d' ' -f1)"
    mv "$file" "$blobs/$digest"
    echo "$digest"
}

# This builds the layer: one file, owned by root. The tar flags make
# the archive depend only on its contents (fixed timestamps, numeric
# ownership, sorted names), so rebuilding an unchanged binary produces
# a byte-identical layer, and therefore the same digest.
rootfs="$dist/rootfs"
rm -rf "$rootfs"
mkdir -p "$rootfs"
cp "$dist/$binary" "$rootfs/$binary"

# This stages TLS trust, for the programs that connect out of the
# cluster. There is no base image here, so there is no root
# certificate store unless this script adds one. Without it, Go's
# crypto/x509 rejects every certificate as unknown. Consumers whose
# binaries fetch over HTTPS (the operators, polling and downloading
# from release channels) pass CA_BUNDLE, and the machine's own
# vendored bundle lands at the path Go checks first on Linux.
if [ -n "${CA_BUNDLE:-}" ]; then
    mkdir -p "$rootfs/etc/ssl/certs"
    cp "$CA_BUNDLE" "$rootfs/etc/ssl/certs/ca-certificates.crt"
fi

# This stages the PCI naming database, for the machine operator's
# device inventory (machine-operator/dra.go). It is part of the
# operator's own image rather than a hostPath mount of the OS's
# copy, on purpose. A DaemonSet template applies fleet-wide, while a
# fleet mid-upgrade runs mixed OS versions, so the template must
# never mount a path that some node's OS lacks. A naming database
# that shares a version with the binary reading it can never disagree
# with that binary either.
if [ -n "${PCI_IDS:-}" ]; then
    mkdir -p "$rootfs/usr/share/hwdata"
    cp "$PCI_IDS" "$rootfs/usr/share/hwdata/pci.ids"
fi
tar --create --file "$dist/layer.tar" \
    --sort=name --mtime='@0' --owner=0 --group=0 --numeric-owner \
    -C "$rootfs" .
layer_size="$(stat -c%s "$dist/layer.tar")"
layer_digest="$(add_blob "$dist/layer.tar")"

# This writes the config: the runtime half of the image. diff_ids are
# digests of the uncompressed layer tars. This image's layer is
# stored uncompressed, so the same digest appears in both the
# manifest and here. There is no base image and no shell to name, so
# the Entrypoint is the entire runtime configuration. A pod that
# needs to vary its behavior varies its args instead, the way the
# machine-logs DaemonSet passes each container its verb.
cat >"$dist/config.json" <<EOF
{
  "created": "1970-01-01T00:00:00Z",
  "architecture": "amd64",
  "os": "linux",
  "config": {
    "Entrypoint": ["/$binary"]
  },
  "rootfs": {
    "type": "layers",
    "diff_ids": ["sha256:$layer_digest"]
  }
}
EOF
config_size="$(stat -c%s "$dist/config.json")"
config_digest="$(add_blob "$dist/config.json")"

# This writes the manifest: the config plus the layers, each named by
# digest and size.
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

# This writes the index: the entry point a consumer reads first. The
# containerd annotation is how the image keeps its name through an
# import. An OCI layout has no registry to imply a name, so the
# reference travels inside the archive itself.
#
# The same manifest is named twice: two references to one image. The
# versioned tag says what this build is. The stable "installed" tag
# is the one the OS workloads pin (each operator's and the log
# relays' manifests). Every release tags its own build "installed",
# so one unchanging pod spec resolves, on every node, to the build
# that node's own OS imported. Content addressing makes this dual
# naming free: both names point at the same digest.
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
        "io.containerd.image.name": "$image:$version",
        "org.opencontainers.image.ref.name": "$version"
      }
    },
    {
      "mediaType": "application/vnd.oci.image.manifest.v1+json",
      "digest": "sha256:$manifest_digest",
      "size": $manifest_size,
      "platform": {"architecture": "amd64", "os": "linux"},
      "annotations": {
        "io.containerd.image.name": "$image:installed",
        "org.opencontainers.image.ref.name": "installed"
      }
    }
  ]
}
EOF

# The marker file declares that this directory is an OCI image
# layout. The whole layout then becomes one archive.
echo '{"imageLayoutVersion": "1.0.0"}' >"$layout/oci-layout"
tar --create --file "$dist/$binary-image.tar" \
    --sort=name --mtime='@0' --owner=0 --group=0 --numeric-owner \
    -C "$layout" oci-layout index.json blobs

echo "$image:$version:"
du -sh "$dist/$binary-image.tar"
