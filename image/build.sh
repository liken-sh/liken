#!/usr/bin/env bash
#
# Assemble the liken image: an initramfs the kernel unpacks into RAM.
#
# This directory is organized as a mirror of the filesystem it
# produces: image/etc/rancher/k3s/config.yaml is the file the machine
# sees at /etc/rancher/k3s/config.yaml, and so on — configuration lives
# at its destination path, reviewable with plain ls. The build stages
# that tree plus the built and vendored artifacts in dist/root, then
# archives it with cpio. The complete inventory of the operating
# system:
#
#   /liken                        init; the kernel runs it as PID 1
#                                 (rdinit=/liken)
#   /etc/liken/machine.yaml       who this machine is
#   /etc/liken/modules.conf       which kernel modules init loads
#   /lib/modules/<release>/       those modules and their dependencies,
#                                 exactly as Ubuntu built them
#   /bin/k3s                      all of Kubernetes, in one binary
#   /etc/rancher/k3s/config.yaml  how k3s should behave
#   /etc/ssl/certs/               CA certificates, so pulling images
#                                 over TLS can verify who it's talking to
#   /etc/passwd, group,           the Unix identity files: this machine
#     subuid, subgid              has exactly two users, root and nobody,
#                                 and no way to log in as either. kubelet
#                                 reads passwd and the sub-ID ranges to
#                                 map container users into namespaces
#   /var/lib/rancher/k3s/         the cluster's certificate authorities,
#     server/tls/                 minted by the identity domain. k3s
#                                 finds them here and signs its leaf
#                                 certs from them instead of inventing
#                                 its own roots — which is what makes an
#                                 operator's kubeconfig computable
#                                 offline (identity/mint.sh). It also
#                                 means the image contains private keys:
#                                 whoever holds this file owns the
#                                 cluster it boots
#
# Nothing else. No shell, no coreutils, no libc: every file above is
# either written in this repo or vendored by a pinned, verified fetch.

set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
kernel_version="$(cat "$here/../kernel/VERSION")"
k3s_version="$(cat "$here/../k3s/VERSION")"
kdist="$here/../kernel/dist/$kernel_version"
release="$(cat "$kdist/release")"

root="$here/dist/root"
rm -rf "$here/dist"
mkdir -p "$root/etc/ssl/certs" "$root/bin"

cp -r "$here/etc" "$root/"
cp "$here/../init/dist/liken" "$root/liken"
cp "$here/../k3s/dist/$k3s_version/k3s" "$root/bin/k3s"

# The pre-minted certificate authorities, placed exactly where k3s
# looks before generating its own.
mkdir -p "$root/var/lib/rancher/k3s/server"
cp -r "$here/../identity/dist/tls" "$root/var/lib/rancher/k3s/server/tls"

# The CA bundle comes from the build host — every Linux machine has the
# Mozilla trust store at this conventional path. Vendoring it with a
# pinned fetch like the kernel and k3s would be more honest about where
# trust comes from; for a bundle this stable, the host's copy serves.
cp /etc/ssl/certs/ca-certificates.crt "$root/etc/ssl/certs/ca-certificates.crt"

# The image carries only the modules the machine will load — the list
# in etc/liken/modules.conf — plus everything they depend on. The
# host's modprobe
# resolves each name against the vendored kernel's depmod index
# (--show-depends prints one "insmod <path>" line per file, dependencies
# first) without loading anything anywhere.
mkdir -p "$root/lib/modules/$release"
while IFS= read -r name; do
    [[ -z "$name" || "$name" == \#* ]] && continue
    modprobe -d "$kdist" -S "$release" --show-depends "$name"
done <"$here/etc/liken/modules.conf" |
    awk '$1 == "insmod" { print $2 }' |
    sort -u |
    while IFS= read -r file; do
        rel="${file#"$kdist"/lib/modules/"$release"/}"
        mkdir -p "$root/lib/modules/$release/$(dirname "$rel")"
        cp "$file" "$root/lib/modules/$release/$rel"
    done

# depmod indexes what actually shipped, so init's dependency resolution
# (which reads modules.dep) agrees exactly with the files present.
# modules.builtin tells depmod which names live inside vmlinuz itself;
# modules.order settles ambiguity when two modules claim one alias.
cp "$kdist/lib/modules/$release/modules.builtin" \
   "$kdist/lib/modules/$release/modules.builtin.modinfo" \
   "$kdist/lib/modules/$release/modules.order" \
   "$root/lib/modules/$release/"
depmod --basedir "$root" "$release"

# cpio, flag by flag: -o creates an archive from filenames on stdin
# (the archive's contents are an explicit, reviewable stream); -H newc
# is the one format the kernel's unpacker accepts; -R +0:+0 makes root
# own everything, whoever ran the build.
(cd "$root" && find . | cpio --quiet -o -H newc -R +0:+0) >"$here/dist/liken.cpio"

echo "image for kernel $release, k3s $k3s_version:"
du -sh "$here/dist/liken.cpio"
