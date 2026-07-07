#!/usr/bin/env bash
#
# Assemble the install image: the same OS, plus a copy of itself to
# install.
#
# An installer must contain the exact bytes it installs, and an
# archive cannot contain a finished copy of itself. So the install
# image is two archives concatenated: the ordinary liken.cpio (the
# running system: init, k3s, everything), followed by a small wrapper
# archive carrying the release payload at /usr/share/liken/release.
# The kernel's initramfs unpacker processes concatenated cpio archives
# in order, one after another, into the same filesystem. This is the
# same mechanism early CPU-microcode updates use.
#
# The payload is three files: vmlinuz and liken.cpio, byte-identical
# to what the machine is booting right now, and release.yaml naming
# both by sha256 digest and size. The installer (init/install.go)
# verifies the payload against the document before copying anything,
# and verifies the copies on the slot after. The release document is
# the same one a release server publishes, so the installer and the
# over-the-network upgrade path share one format.
set -euo pipefail

here="$(cd "$(dirname "$0")" && pwd)"
kernel_version="$(<"$here/../kernel/VERSION")"

# Overridable for the same reason build.sh's are: a release build
# wraps its own liken.cpio, under its own version, in its own tree.
liken_version="${LIKEN_VERSION:-$(<"$here/../VERSION")}"
dist="${DIST:-$here/dist}"

vmlinuz="$here/../kernel/dist/$kernel_version/vmlinuz"
cpio="$dist/liken.cpio"

payload="$dist/install-root"
release="$payload/usr/share/liken/release"
rm -rf "$payload"
mkdir -p "$release"

cp "$vmlinuz" "$release/vmlinuz"
cp "$cpio" "$release/liken.cpio"

# The release document is generated from the payload itself so the
# two can never disagree. sha256sum and stat read the copies, which
# are the bytes that actually travel in the archive.
sha_vmlinuz="$(sha256sum "$release/vmlinuz" | cut -d' ' -f1)"
sha_cpio="$(sha256sum "$release/liken.cpio" | cut -d' ' -f1)"
cat >"$release/release.yaml" <<EOF
apiVersion: liken.sh/v1alpha1
kind: Release
metadata:
  name: $liken_version
artifacts:
  - name: vmlinuz
    sha256: $sha_vmlinuz
    size: $(stat -c %s "$release/vmlinuz")
  - name: liken.cpio
    sha256: $sha_cpio
    size: $(stat -c %s "$release/liken.cpio")
EOF

# Pack the wrapper archive, then concatenate the two. The cpio flags
# are the same as build.sh's: newc is the format the kernel accepts,
# and root owns everything regardless of who ran the build.
(cd "$payload" && find . | cpio --quiet -o -H newc -R +0:+0) >"$dist/install-wrapper.cpio"
cat "$cpio" "$dist/install-wrapper.cpio" >"$dist/install.cpio"

echo "install image for liken $liken_version:"
du -sh "$dist/install.cpio"
