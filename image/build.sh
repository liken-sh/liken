#!/usr/bin/env bash
#
# Assemble the generic liken system: the operating system with
# nothing about any one deployment inside, produced as two artifacts.
#
# liken.sqfs is the system image: the whole OS tree as a read-only,
# zstd-compressed squashfs. A machine never unpacks it — init
# loop-mounts it (from the boot slot, or from RAM when the boot
# loader delivered it) and makes it the root filesystem, with a small
# bounded tmpfs overlaid for the runtime's writes. The OS therefore
# costs page cache, not a permanent copy of itself in memory.
#
# boot.cpio is the initramfs, and it is deliberately tiny: init and
# the few modules the early boot needs (boot-modules.conf). It is all
# the boot loader stages in RAM, which is what lets liken boot a
# 1 GB machine with room to spare.
#
# What makes the system a *deployment's* — the cluster identity, the
# manifests, the machines' declared kernel modules — arrives as a
# second cpio archive, the deployment layer (image/layer.go), loaded
# after boot.cpio: the kernel unpacks both into rootfs, and init
# carries the layer's files onto the mounted root. The split is what
# makes liken releasable: the system image's digest never changes
# with the deployment, so a public release can publish it, and
# producing a bootable image from a release is composition, not
# compilation. An image with no layer is still a valid machine:
# everything defaults, with DHCP, and no cluster to form or join.
#
# This directory is organized as a mirror of the filesystem it
# produces: image/etc/rancher/k3s/config.yaml is the file the machine
# sees at /etc/rancher/k3s/config.yaml, and so on. Configuration lives
# at its destination path, reviewable with plain ls. The build stages
# that tree plus the built and vendored artifacts in dist/root, then
# archives it with cpio. The complete inventory of the operating
# system:
#
#   /liken                        init; the kernel runs it as PID 1
#                                 (rdinit=/liken)
#   /etc/liken/modules.conf       which kernel modules init loads for
#                                 the OS's own needs
#   /lib/modules/<release>/       those modules, the features' modules,
#                                 and everything they depend on,
#                                 exactly as Ubuntu built them
#   /bin/k3s                      all of Kubernetes, in one binary
#   /etc/rancher/k3s/config.yaml  k3s's configuration for leaders
#   /etc/rancher/k3s/agent.yaml   the followers' configuration (init
#                                 starts the role the cluster manifest
#                                 implies, and each role reads its own
#                                 file plus a boot-derived drop-in)
#   /sbin/iptables (and the       the netfilter userspace kube-proxy and
#     related tools)
#                                 the CNI exec to program the kernel's
#                                 packet filter: one static multi-call
#                                 binary (vendored from k3s-root by
#                                 xtables/fetch.sh) under each of the
#                                 names it answers to. k3s puts /sbin
#                                 ahead of its own bundled tools on the
#                                 PATHs it builds, so these win, which
#                                 matters because the bundled iptables
#                                 is a #!/bin/sh script, and there is no
#                                 shell here to run it
#   /sbin/mke2fs                  makes ext4 filesystems on the disks
#                                 init claims; static, vendored from
#                                 gokrazy's reproducible e2fsprogs build
#                                 (see e2fsprogs/fetch.sh). It carries
#                                 its own built-in default profile, so
#                                 no mke2fs.conf ships
#   /sbin/iscsiadm, /sbin/iscsid  the iSCSI initiator userspace, the
#     and /etc/iscsi/             host half of the iscsi feature:
#                                 static, built from pinned source by
#                                 open-iscsi/fetch.sh. Shipped in every
#                                 image and inert until the cluster
#                                 document declares the feature; CSI
#                                 drivers chroot into the host to exec
#                                 iscsiadm, so /sbin is the contract
#   /sbin/mount.nfs (and its      the NFSv4 client, the whole host
#     mount.nfs4 alias)           half of the nfs feature: static,
#                                 built by nfs-utils/fetch.sh. The
#                                 kernel's mount path execs it as the
#                                 nfs filesystem's mount helper
#   /etc/mtab                     the compatibility symlink mount
#                                 helpers require, pointing at the
#                                 kernel's own mount table
#   /etc/liken/features/          each opt-in feature's per-boot
#                                 inputs, by slug: its kernel module
#                                 list and, for features with a
#                                 workload, its manifests. Init acts on
#                                 a feature's directory only when the
#                                 cluster document declares it
#                                 (init/features.go)
#   /etc/ssl/certs/               CA certificates (vendored by the trust
#                                 domain), so pulling images over TLS
#                                 can verify who it's talking to
#   /etc/passwd, group,           the Unix identity files: this machine
#     subuid, subgid              has exactly two users, root and nobody,
#                                 and no way to log in as either. kubelet
#                                 reads passwd and the sub-ID ranges to
#                                 map container users into namespaces
#   /var/lib/rancher/k3s/         the liken CRDs and the operators'
#     server/manifests/           deployments: k3s applies everything in
#                                 this directory to the cluster at
#                                 startup, so the OS's own resources
#                                 arrive with no kubectl step
#   /var/lib/rancher/k3s/         the liken operators' container images
#     agent/images/               as OCI tarballs (built by hand in
#                                 image/oci.sh); k3s imports every
#                                 archive here into containerd at start,
#                                 so the machine never pulls its own OS
#                                 components from a registry
#
# That is the complete list. There is no shell, no coreutils, and no
# libc: every file above is either written in this repo or vendored by
# a pinned, verified fetch.

set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
kernel_version="$(cat "$here/../kernel/VERSION")"
k3s_version="$(cat "$here/../k3s/VERSION")"
xtables_version="$(cat "$here/../xtables/VERSION")"
kdist="$here/../kernel/dist/$kernel_version"
release="$(cat "$kdist/release")"

# The version this image claims to be, where the archive lands, and
# where the liken binary and operator images come from. All of these can
# be overridden from the environment, because the releases domain
# assembles release-stamped images through this same script, from its
# own copies of the inputs, into its own tree (see the Makefile).
liken_version="${LIKEN_VERSION:?LIKEN_VERSION must be set; the Makefile passes it via version.mk}"
dist="${DIST:-$here/dist}"
init_dist="${INIT_DIST:-$here/../init/dist}"
machine_operator_dist="${MACHINE_OPERATOR_DIST:-$here/../machine-operator/dist}"
cluster_operator_dist="${CLUSTER_OPERATOR_DIST:-$here/../cluster-operator/dist}"
logs_dist="${LOGS_DIST:-$here/../logs/dist}"
openiscsi_version="$(cat "$here/../open-iscsi/VERSION")"
openiscsi_dist="${OPENISCSI_DIST:-$here/../open-iscsi/dist/$openiscsi_version}"

root="$dist/root"
rm -rf "$dist"
mkdir -p "$root/etc/ssl/certs" "$root/bin" "$root/sbin"

cp -r "$here/etc" "$root/"
cp "$init_dist/liken" "$root/liken"
cp "$here/../k3s/dist/$k3s_version/k3s" "$root/bin/k3s"

# The netfilter userspace: one real static binary, then a symlink per
# tool name. The multi-call binary reads argv[0] to decide which tool
# to behave as. Only the legacy variant ships, matching the iptable_*
# kernel modules in modules.conf.
cp "$here/../xtables/dist/$xtables_version/bin/xtables-legacy-multi" "$root/sbin/"
for tool in iptables iptables-save iptables-restore \
            ip6tables ip6tables-save ip6tables-restore; do
    ln -s xtables-legacy-multi "$root/sbin/$tool"
done

# mke2fs creates the ext4 filesystems on the disks init claims.
e2fsprogs_version="$(cat "$here/../e2fsprogs/VERSION")"
cp "$here/../e2fsprogs/dist/$e2fsprogs_version/mke2fs" "$root/sbin/mke2fs"

# The iSCSI initiator userspace, the host half of the iscsi feature
# (open-iscsi/fetch.sh explains the static build). It ships in every
# image whether or not the deployment declares the feature: the
# payload is a few megabytes of inert bytes until the cluster document
# opts in, and shipping it unconditionally is what keeps enabling a
# feature a runtime act instead of an image rebuild. CSI drivers
# chroot into the host and exec iscsiadm from the host's own PATH, so
# /sbin is the contract; iscsid ships beside it so the feature's
# DaemonSet and the host tool are always the same build. The /etc/iscsi
# directory is the initiator's home: iscsid refuses to start without
# its config file, and init writes the machine's initiator name beside
# it at boot when the feature is declared (init/features.go).
cp "$openiscsi_dist/iscsiadm" "$root/sbin/iscsiadm"
cp "$openiscsi_dist/iscsid" "$root/sbin/iscsid"
mkdir -p "$root/etc/iscsi"
cp "$here/../open-iscsi/iscsid.conf" "$root/etc/iscsi/iscsid.conf"

# The NFS client, the host half of the nfs feature (nfs-utils/fetch.sh
# explains the static build), shipped in every image whether or not
# the deployment declares the feature: inert bytes are cheap, and
# shipping them unconditionally keeps enabling a feature a runtime
# act instead of an image rebuild. The kernel's mount syscall path
# execs /sbin/mount.<fstype> as a filesystem's mount helper, so the
# one binary answers under both of its names: mount -t nfs and
# mount -t nfs4 both reach it.
nfsutils_version="$(cat "$here/../nfs-utils/VERSION")"
cp "$here/../nfs-utils/dist/$nfsutils_version/mount.nfs" "$root/sbin/mount.nfs"
ln -s mount.nfs "$root/sbin/mount.nfs4"

# /etc/mtab, the file where mount tools recorded mounts back when the
# kernel didn't expose them; it has been a compatibility symlink to
# the kernel's own table on every mainstream distribution since about
# 2011. It matters here because mount helpers still honor the old
# contract: after a successful mount syscall, mount.nfs goes to
# record the mount in mtab, and only the file being a symlink tells
# it the kernel already keeps the table. On an /etc with no mtab at
# all, that bookkeeping retries forever: the mount itself succeeds in
# milliseconds while the helper never exits, so the machine looks
# hung.
ln -s /proc/self/mounts "$root/etc/mtab"

# The cluster's certificate authorities and join token deliberately
# do not appear here: they are the deployment layer's cargo
# (image/layer.go), which is exactly why this archive can be
# published without handing out a cluster.

# The liken API and the programs that operate it, all delivered
# through k3s's own mechanisms: the manifests go where k3s
# auto-applies them, and the OCI images go where k3s auto-imports
# them. The manifests come from each domain: the machine and cluster
# domains carry their CRDs, the kubernetes domain carries the
# liken-system namespace, and each operator and the log relays carry
# their own deployment beside their code. The LIKEN_VERSION
# substitution stamps each manifest with the release it shipped in,
# which is what the pod steward compares against a machine's running
# version.
mkdir -p "$root/var/lib/rancher/k3s/server/manifests"
for manifest in "$here"/../machine/manifests/*.yaml \
        "$here"/../cluster/manifests/*.yaml \
        "$here"/../kubernetes/manifests/*.yaml \
        "$here"/../machine-operator/manifests/*.yaml \
        "$here"/../cluster-operator/manifests/*.yaml \
        "$here"/../logs/manifests/*.yaml; do
    sed "s/LIKEN_VERSION/$liken_version/g" "$manifest" \
        >"$root/var/lib/rancher/k3s/server/manifests/$(basename "$manifest")"
done
mkdir -p "$root/var/lib/rancher/k3s/agent/images"
cp "$machine_operator_dist/liken-machine-operator-image.tar" \
   "$root/var/lib/rancher/k3s/agent/images/liken-machine-operator.tar"
cp "$cluster_operator_dist/liken-cluster-operator-image.tar" \
   "$root/var/lib/rancher/k3s/agent/images/liken-cluster-operator.tar"
cp "$logs_dist/liken-logs-image.tar" \
   "$root/var/lib/rancher/k3s/agent/images/liken-logs.tar"

# Each opt-in feature's per-boot inputs, staged under
# /etc/liken/features by slug: the feature's kernel module list and,
# for a feature with a workload, its manifests. These are deliberately
# not in the auto-deploy directory above: everything there applies on
# every boot, while a feature's workload applies only when the cluster
# document declares it, and init is the gate (init/features.go). The
# iscsid container image does go in agent/images with the others,
# because an imported-but-unused image is inert, and importing is not
# deploying.
mkdir -p "$root/etc/liken/features/iscsi/manifests"
cp "$here/../open-iscsi/modules.conf" "$root/etc/liken/features/iscsi/modules.conf"
for manifest in "$here"/../open-iscsi/manifests/*.yaml; do
    sed "s/LIKEN_VERSION/$liken_version/g" "$manifest" \
        >"$root/etc/liken/features/iscsi/manifests/$(basename "$manifest")"
done
cp "$openiscsi_dist/iscsid-image.tar" \
   "$root/var/lib/rancher/k3s/agent/images/liken-iscsid.tar"
mkdir -p "$root/etc/liken/features/nfs"
cp "$here/../nfs-utils/modules.conf" "$root/etc/liken/features/nfs/modules.conf"

# The machine's trust store, vendored by the trust domain (where these
# roots come from is explained in trust/fetch.sh). The staged name is
# the conventional path Go's crypto/x509 and most TLS stacks probe.
trust_version="$(cat "$here/../trust/VERSION")"
cp "$here/../trust/dist/$trust_version/cacert.pem" \
   "$root/etc/ssl/certs/ca-certificates.crt"

# The PCI naming database (see hwdata/fetch.sh): what lets the
# unclaimed-hardware report say "Red Hat, Inc. Virtio 1.0 GPU"
# instead of "1af4:1050". Staged at hwdata's conventional path; the
# dependency is soft, so a reader missing this file degrades to
# numeric IDs rather than failing.
hwdata_version="$(cat "$here/../hwdata/VERSION")"
mkdir -p "$root/usr/share/hwdata"
cp "$here/../hwdata/dist/$hwdata_version/pci.ids" \
   "$root/usr/share/hwdata/pci.ids"

# The image carries only the modules the machines will load, plus
# everything those depend on. Two lists feed this: the OS's own fixed
# needs (etc/liken/modules.conf) and whatever extra modules the
# deployment's Machine manifests declare (spec.modules), both shipped
# by the same resolution below. The host's modprobe resolves each name
# against the vendored kernel's depmod index (--show-depends prints
# one "insmod <path>" line per file, dependencies first) without
# loading anything anywhere. A name the vendored kernel doesn't have
# fails the build right here, which is the point: a deployment learns
# about a typo'd module at build time, not on a booted fleet.
ship_modules() {
    local dest="$1"
    while IFS= read -r name; do
        [[ -z "$name" || "$name" == \#* ]] && continue
        modprobe -d "$kdist" -S "$release" --show-depends "$name"
    done |
        awk '$1 == "insmod" { print $2 }' |
        sort -u |
        while IFS= read -r file; do
            rel="${file#"$kdist"/lib/modules/"$release"/}"
            mkdir -p "$dest/lib/modules/$release/$(dirname "$rel")"
            cp "$file" "$dest/lib/modules/$release/$rel"
        done
}
mkdir -p "$root/lib/modules/$release"
ship_modules "$root" <"$here/etc/liken/modules.conf"

# The features' kernel halves ship unconditionally too, one list per
# feature (staged above under /etc/liken/features); whether they load
# is the cluster document's call, made at boot, never at build.
ship_modules "$root" <"$here/../open-iscsi/modules.conf"
ship_modules "$root" <"$here/../nfs-utils/modules.conf"

# The deployment's declared modules (spec.modules) are not here: the
# deployment layer ships them, closure and all, resolved against the
# same index by image/layer.go.

# depmod indexes what actually shipped, so init's dependency resolution
# (which reads modules.dep) agrees exactly with the files present.
# A deployment layer that adds modules overrides this index with the
# kernel's complete one, so the composed system resolves everything
# actually present (image/layer.go explains the override).
# modules.builtin tells depmod which names live inside vmlinuz itself;
# modules.order settles ambiguity when two modules claim one alias.
cp "$kdist/lib/modules/$release/modules.builtin" \
   "$kdist/lib/modules/$release/modules.builtin.modinfo" \
   "$kdist/lib/modules/$release/modules.order" \
   "$root/lib/modules/$release/"
depmod --basedir "$root" "$release"

# One index is deliberately *not* depmod's pruned output: the alias
# table ships complete, from the kernel build, covering every module
# the kernel could load — not just the ones aboard. It is the
# unclaimed-hardware report's naming database: when a device shows
# up that nothing drives, the report matches its modalias here to
# say "declare usb_storage" (or "this image doesn't carry it"),
# which a table describing only the shipped modules could never say
# about the very driver that's missing. Nothing loads modules from
# this file — init resolves loads through modules.dep, which still
# describes exactly what shipped.
cp "$kdist/lib/modules/$release/modules.alias" \
   "$root/lib/modules/$release/modules.alias"

# The system image: the staged tree as a read-only, mountable
# filesystem. squashfs because this kernel mounts it with no modules
# at all (CONFIG_SQUASHFS=y, the zstd decompressor and the loop
# device built in), so the boot path needs nothing loaded to reach
# its root. At boot the running root *is* this artifact, loop-mounted
# from the slot and never unpacked: the RAM cost of the OS is the
# page cache, which the kernel reclaims under pressure, instead of a
# permanent tmpfs copy. -all-root makes root own everything, whoever
# ran the build; -noappend replaces rather than appends on rebuild.
mksquashfs "$root" "$dist/liken.sqfs" \
    -comp zstd -all-root -noappend -no-progress -quiet

# The boot archive: the one cpio the boot loader still stages in RAM,
# so it carries the minimum that must exist before the system image
# is mounted — init itself, the early boot's few modules
# (boot-modules.conf), and mke2fs. The modules live under
# lib/modules/boot with their own depmod index: a name deliberately
# not the kernel's release string, so when init later carries the
# boot-time files onto the real root, nothing here can shadow the
# system image's complete index at lib/modules/<release>.
#
# mke2fs rides along for the install boot, which runs from this
# archive alone: a machine whose data roles share the boot disk (a
# cloud machine with one disk is the common case) claims and formats
# them during the install, and formatting ext4 is mke2fs's job.
# Ordinary boots never reach this copy — the system image carries its
# own at the same path, and the boot archive's files stay off the
# real root.
boot_root="$dist/boot-root"
mkdir -p "$boot_root/lib/modules/$release" "$boot_root/sbin"
cp "$init_dist/liken" "$boot_root/liken"
cp "$here/../e2fsprogs/dist/$e2fsprogs_version/mke2fs" "$boot_root/sbin/mke2fs"
ship_modules "$boot_root" <"$here/boot-modules.conf"
cp "$kdist/lib/modules/$release/modules.builtin" \
   "$kdist/lib/modules/$release/modules.builtin.modinfo" \
   "$kdist/lib/modules/$release/modules.order" \
   "$boot_root/lib/modules/$release/"
depmod --basedir "$boot_root" "$release"
mv "$boot_root/lib/modules/$release" "$boot_root/lib/modules/boot"

# The list itself rides along, so init loads exactly what the build
# shipped — one file feeds both decisions.
cp "$here/boot-modules.conf" "$boot_root/lib/modules/boot/boot-modules.conf"

(cd "$boot_root" && find . | cpio --quiet -o -H newc -R +0:+0) >"$dist/boot.cpio"
rm -rf "$boot_root"

echo "image for kernel $release, k3s $k3s_version:"
du -sh "$dist/liken.sqfs" "$dist/boot.cpio"
