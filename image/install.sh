#!/usr/bin/env bash
#
# Assemble the install image: the same OS, carrying itself as cargo.
#
# An installer must contain the exact bytes it installs, and an
# archive cannot contain its own final self — so the install image is
# two archives concatenated: the ordinary liken.cpio (the running
# system: init, k3s, everything), followed by a small wrapper archive
# carrying the release payload at /usr/share/liken/release. The
# kernel's initramfs unpacker processes concatenated cpio archives in
# order, one after another, into the same filesystem — the exact
# mechanism early CPU-microcode updates ride, put to friendlier use.
#
# The payload is three files: vmlinuz and liken.cpio, byte-identical
# to what the machine is booting right now, and release.yaml naming
# both by sha256 digest and size. The installer (init/install.go)
# verifies the payload against the document before copying anything,
# and verifies the copies on the slot after — the release document is
# the same one a release server publishes, so the installer and the
# over-the-network upgrade path speak one format.
set -euo pipefail

here="$(cd "$(dirname "$0")" && pwd)"
kernel_version="$(<"$here/../kernel/VERSION")"
liken_version="$(<"$here/../VERSION")"

vmlinuz="$here/../kernel/dist/$kernel_version/vmlinuz"
cpio="$here/dist/liken.cpio"

payload="$here/dist/install-root"
release="$payload/usr/share/liken/release"
rm -rf "$payload"
mkdir -p "$release"

cp "$vmlinuz" "$release/vmlinuz"
cp "$cpio" "$release/liken.cpio"

# The release document, generated from the payload itself so the two
# can never disagree. sha256sum and stat read the *copies*, the bytes
# that actually ride in the archive.
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

# The wrapper archive, then the concatenation. Same cpio flags as
# build.sh: newc is the format the kernel accepts, and root owns
# everything regardless of who ran the build.
(cd "$payload" && find . | cpio --quiet -o -H newc -R +0:+0) >"$here/dist/install-wrapper.cpio"
cat "$cpio" "$here/dist/install-wrapper.cpio" >"$here/dist/install.cpio"

echo "install image for liken $liken_version:"
du -sh "$here/dist/install.cpio"
