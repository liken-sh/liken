#!/usr/bin/env bash
#
# This script assembles the generic liken system: the operating
# system with nothing about any one deployment inside. It produces
# two artifacts.
#
# liken.sqfs is the system image: the whole OS tree as a read-only,
# zstd-compressed squashfs. A machine never unpacks it. Instead, init
# loop-mounts it (from the boot slot, or from RAM when the boot
# loader delivered it) and makes it the root filesystem, with a
# small, bounded tmpfs overlaid for the runtime's writes. The OS
# therefore costs page cache in memory, not a permanent copy of
# itself.
#
# boot.cpio is the initramfs, and it is deliberately small: init and
# the few modules early boot needs (boot-modules.conf). This is all
# the boot loader stages in RAM, and it is what lets liken boot a
# 1 GB machine with room to spare.
#
# A second cpio archive, the deployment layer (image/layer.go),
# carries what makes the system belong to one deployment: the
# cluster identity, the manifests, and the machines' declared kernel
# modules. The boot loader loads the deployment layer after
# boot.cpio. The kernel unpacks both archives into rootfs, and init
# copies the layer's files onto the mounted root. This split is what
# makes liken releasable: the system image's digest never changes
# with the deployment, so a public release can publish it, and
# producing a bootable image from a release is composition, not
# compilation. An image with no layer is still a valid machine:
# everything defaults, the machine uses DHCP, and it has no cluster
# to form or join.
#
# This directory mirrors the filesystem it produces. For example,
# image/etc/rancher/k3s/config.yaml is the file the machine sees at
# /etc/rancher/k3s/config.yaml, and so on. Configuration lives at its
# destination path, so a person can review it with plain ls. The
# build stages that tree, plus the built and vendored artifacts, in
# dist/root, then archives it with cpio. Below is the complete
# inventory of the operating system:
#
#   /liken                        init. The kernel runs it as PID 1
#                                 (rdinit=/liken)
#   /etc/liken/modules.conf       lists the kernel modules init loads
#                                 for the OS's own needs
#   /lib/modules/<release>/       the kernel build's complete module
#                                 tree, with depmod's indexes, exactly
#                                 as Ubuntu built it. Every module
#                                 ships inert; a module costs disk
#                                 space only, until init or a declared
#                                 spec.modules entry loads it
#   /lib/firmware/                the driver blobs those modules can
#                                 request at probe time, derived from
#                                 the module tree by the
#                                 linux-firmware domain, with the
#                                 WHENCE ledger and license texts
#                                 beside them
#   /bin/k3s                      all of Kubernetes, in one binary
#   /etc/rancher/k3s/config.yaml  k3s's configuration for leaders
#   /etc/rancher/k3s/agent.yaml   the followers' configuration. Init
#                                 starts the role the cluster manifest
#                                 implies, and each role reads its own
#                                 file plus a boot-derived drop-in
#   /sbin/iptables (and the       the netfilter userspace that
#     related tools)
#                                 kube-proxy and the CNI use to
#                                 program the kernel's packet filter:
#                                 one static multi-call binary
#                                 (vendored from k3s-root by
#                                 xtables/fetch.sh), installed under
#                                 each name it answers to. k3s puts
#                                 /sbin ahead of its own bundled tools
#                                 on the PATHs it builds, so these
#                                 binaries win. This matters because
#                                 the bundled iptables is a
#                                 #!/bin/sh script, and this system
#                                 has no shell to run it
#   /sbin/mke2fs                  makes ext4 filesystems on the disks
#                                 init claims. Static, vendored from
#                                 gokrazy's reproducible e2fsprogs
#                                 build (see e2fsprogs/fetch.sh). It
#                                 carries its own built-in default
#                                 profile, so the system ships no
#                                 mke2fs.conf
#   /sbin/iscsiadm, /sbin/iscsid  the iSCSI initiator userspace, the
#     and /etc/iscsi/             host half of the iscsi feature.
#                                 Static, built from pinned source by
#                                 open-iscsi/fetch.sh. Every image
#                                 ships it, and it stays inert until
#                                 the cluster document declares the
#                                 feature. CSI drivers chroot into the
#                                 host to run iscsiadm, so /sbin is
#                                 the contract
#   /sbin/mount.nfs (and its      the NFSv4 client, the whole host
#     mount.nfs4 alias)           half of the nfs feature. Static,
#                                 built by nfs-utils/fetch.sh. The
#                                 kernel's mount path runs it as the
#                                 nfs filesystem's mount helper
#   /etc/mtab                     the compatibility symlink mount
#                                 helpers require. It points at the
#                                 kernel's own mount table
#   /etc/liken/features/          each opt-in feature's per-boot
#                                 inputs, by slug: its kernel module
#                                 list and, for a feature with a
#                                 workload, its manifests. Init acts
#                                 on a feature's directory only when
#                                 the cluster document declares it
#                                 (init/features.go)
#   /etc/ssl/certs/               the CA certificates (vendored by the
#                                 trust domain), so a program pulling
#                                 images over TLS can verify the
#                                 server's certificate
#   /etc/passwd, group,           the Unix identity files. This machine
#     subuid, subgid              has exactly two users, root and
#                                 nobody, and no way to log in as
#                                 either one. kubelet reads passwd and
#                                 the sub-ID ranges to map container
#                                 users into namespaces
#   /var/lib/rancher/k3s/         the liken CRDs and the operators'
#     server/manifests/           deployments. k3s applies everything
#                                 in this directory to the cluster at
#                                 startup, so the OS's own resources
#                                 arrive without a separate kubectl
#                                 step
#   /var/lib/rancher/k3s/         the liken operators' container images
#     agent/images/               as OCI tarballs (built by hand in
#                                 image/oci.sh). k3s imports every
#                                 archive here into containerd at
#                                 start, so the machine never pulls
#                                 its own OS components from a
#                                 registry
#
# That is the complete list. This system has no shell, no coreutils,
# and no libc. Every file above is either written in this repo, or
# vendored by a pinned, verified fetch.

set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
kernel_version="$(cat "$here/../kernel/VERSION")"
k3s_version="$(cat "$here/../k3s/VERSION")"
xtables_version="$(cat "$here/../xtables/VERSION")"
kdist="$here/../kernel/dist/$kernel_version"
release="$(cat "$kdist/release")"

# These variables set the version this image claims to be, where the
# archive lands, and where the liken binary and operator images come
# from. The environment can override all of them, because the
# releases domain uses this same script to assemble release-stamped
# images, running it from its own copies of the inputs into its own
# tree (see the Makefile).
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

# This is the netfilter userspace: one real static binary, then a
# symlink for each tool name. The multi-call binary reads argv[0] to
# decide which tool to act as. The build ships only the legacy
# variant, matching the iptable_* kernel modules in modules.conf.
cp "$here/../xtables/dist/$xtables_version/bin/xtables-legacy-multi" "$root/sbin/"
for tool in iptables iptables-save iptables-restore \
            ip6tables ip6tables-save ip6tables-restore; do
    ln -s xtables-legacy-multi "$root/sbin/$tool"
done

# mke2fs creates the ext4 filesystems on the disks init claims.
e2fsprogs_version="$(cat "$here/../e2fsprogs/VERSION")"
cp "$here/../e2fsprogs/dist/$e2fsprogs_version/mke2fs" "$root/sbin/mke2fs"

# This is the iSCSI initiator userspace, the host half of the iscsi
# feature (open-iscsi/fetch.sh explains the static build). Every
# image ships it, whether or not the deployment declares the
# feature. Until the cluster document opts in, the payload is a few
# megabytes of inert bytes. Shipping it unconditionally keeps
# enabling a feature a runtime act, not an image rebuild. CSI drivers
# chroot into the host and run iscsiadm from the host's own PATH, so
# /sbin is the contract. iscsid ships beside it, so the feature's
# DaemonSet and the host tool always come from the same build. The
# /etc/iscsi directory holds the initiator's configuration: iscsid
# refuses to start without its config file, and init writes the
# machine's initiator name beside it at boot, when the feature is
# declared (init/features.go).
cp "$openiscsi_dist/iscsiadm" "$root/sbin/iscsiadm"
cp "$openiscsi_dist/iscsid" "$root/sbin/iscsid"
mkdir -p "$root/etc/iscsi"
cp "$here/../open-iscsi/iscsid.conf" "$root/etc/iscsi/iscsid.conf"

# This is the NFS client, the host half of the nfs feature
# (nfs-utils/fetch.sh explains the static build). Every image ships
# it, whether or not the deployment declares the feature. Inert
# bytes cost little, and shipping them unconditionally keeps
# enabling a feature a runtime act, not an image rebuild. The
# kernel's mount syscall path runs /sbin/mount.<fstype> as a
# filesystem's mount helper, so the one binary answers under both of
# its names: mount -t nfs and mount -t nfs4 both reach it.
nfsutils_version="$(cat "$here/../nfs-utils/VERSION")"
cp "$here/../nfs-utils/dist/$nfsutils_version/mount.nfs" "$root/sbin/mount.nfs"
ln -s mount.nfs "$root/sbin/mount.nfs4"

# /etc/mtab is the file where mount tools once recorded mounts, from
# before the kernel exposed them itself. On every mainstream
# distribution since about 2011, it has been a compatibility symlink
# to the kernel's own table. It matters here because mount helpers
# still follow the old contract. After a successful mount syscall,
# mount.nfs tries to record the mount in mtab. Only the file being a
# symlink tells mount.nfs that the kernel already keeps the table. On
# an /etc with no mtab at all, that bookkeeping retries forever: the
# mount itself succeeds in milliseconds, but the helper never exits,
# so the machine looks like it has stopped responding.
ln -s /proc/self/mounts "$root/etc/mtab"

# The cluster's certificate authorities and join token deliberately
# do not appear here. The deployment layer carries them instead
# (image/layer.go). This is exactly why this archive can be
# published without handing out access to a cluster.

# This section stages the liken API and the programs that operate it,
# delivered through k3s's own mechanisms: the manifests go where k3s
# auto-applies them, and the OCI images go where k3s auto-imports
# them. The manifests come from each domain: the machine and cluster
# domains carry their CRDs, the kubernetes domain carries the
# liken-system namespace, and each operator and the log relays carry
# their own deployment beside their code. The LIKEN_VERSION
# substitution stamps each manifest with the release it shipped in.
# The pod steward compares this stamp against a machine's running
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

# This section stages each opt-in feature's per-boot inputs under
# /etc/liken/features, organized by slug: the feature's kernel module
# list and, for a feature with a workload, its manifests. These stay
# out of the auto-deploy directory above on purpose: everything in
# that directory applies on every boot, but a feature's workload
# applies only when the cluster document declares it, and init is the
# gate (init/features.go). The iscsid container image does go in
# agent/images with the others, because an imported but unused image
# stays inert, and importing an image is not the same as deploying it.
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

# This is the machine's trust store, vendored by the trust domain
# (trust/fetch.sh explains where these roots come from). The staged
# name is the conventional path that Go's crypto/x509, and most TLS
# stacks, check first.
trust_version="$(cat "$here/../trust/VERSION")"
cp "$here/../trust/dist/$trust_version/cacert.pem" \
   "$root/etc/ssl/certs/ca-certificates.crt"

# This is the PCI naming database (see hwdata/fetch.sh). It lets the
# unclaimed-hardware report show "Red Hat, Inc. Virtio 1.0 GPU"
# instead of "1af4:1050". It is staged at hwdata's conventional path.
# The dependency is soft: if this file is missing, the report shows
# numeric IDs instead of failing.
hwdata_version="$(cat "$here/../hwdata/VERSION")"
mkdir -p "$root/usr/share/hwdata"
cp "$here/../hwdata/dist/$hwdata_version/pci.ids" \
   "$root/usr/share/hwdata/pci.ids"

# This is the components record: the upstream version of every
# outside component this image carries. The build reads these
# versions from the same VERSION pins that the release document
# publishes (releases/Makefile), so the two can never disagree. It
# rides in the image because a running machine reports its own
# composition in Machine status (init/versions.go), and it should not
# need to contact the channel to learn what it is made of. It sits
# under /usr/share, deliberately outside /etc/liken, because it
# states a fact about the image, not about any deployment of the
# image.
mkdir -p "$root/usr/share/liken"
{
    echo "components:"
    for component in kernel k3s xtables trust e2fsprogs open-iscsi nfs-utils systemd-boot grub hwdata linux-firmware microcode; do
        echo "  - name: $component"
        echo "    version: $(cat "$here/../$component/VERSION")"
    done
} > "$root/usr/share/liken/components.yaml"

# The image ships the kernel build's whole module tree, exactly as
# Ubuntu built and indexed it: about 170 MiB of compressed modules,
# every one of them inert until something loads it. This applies the
# same principle as the feature vocabulary to hardware support:
# enabling a driver is a runtime act (spec.modules), never an image
# rebuild. A machine installed from the public channel can declare
# any module its kernel can load, and a new device is a pure edit. A
# tree pruned to declared modules would tie every image to the
# manifests it was built beside, and would make "I plugged in a
# serial adapter" wait for a release.
#
# The copy carries depmod's complete indexes too, built by
# kernel/fetch.sh at fetch time. The indexes and the modules they
# name describe the same system: modules.dep resolves any module's
# dependency chain, and modules.alias, the naming database for the
# unclaimed-hardware report, names only drivers that are really
# aboard.
mkdir -p "$root/lib/modules"
cp -a "$kdist/lib/modules/$release" "$root/lib/modules/"

# The module lists that init reads at boot still get checked here:
# the OS's own fixed needs (etc/liken/modules.conf) and each
# feature's kernel half (staged above under /etc/liken/features).
# The whole tree makes the copy unconditional, but the host's
# modprobe still resolves each name against the kernel's depmod
# index, without loading anything anywhere. A misspelled name fails
# the build right here, not on a booted fleet.
check_modules() {
    while IFS= read -r name; do
        [[ -z "$name" || "$name" == \#* ]] && continue
        modprobe -d "$kdist" -S "$release" --show-depends "$name" >/dev/null
    done
}
check_modules <"$here/etc/liken/modules.conf"
check_modules <"$here/../open-iscsi/modules.conf"
check_modules <"$here/../nfs-utils/modules.conf"

# The driver firmware ships beside the modules that request it. Real
# hardware's drivers load blobs from /lib/firmware at probe time,
# directly, with no udev involved. The set is derived from the module
# tree above, not curated (linux-firmware/derive.sh explains the
# derivation and the one named exception). The blobs are inert bytes:
# they cost slot space only, until a driver requests one. The kernel
# reads them from this mounted image, so they cost no RAM either.
linuxfirmware_version="$(cat "$here/../linux-firmware/VERSION")"
cp -a "$here/../linux-firmware/dist/$linuxfirmware_version/lib/firmware" \
    "$root/lib/firmware"

# This builds the system image: the staged tree as a read-only,
# mountable filesystem. It uses squashfs because this kernel mounts a
# squashfs with no modules at all (CONFIG_SQUASHFS=y builds the zstd
# decompressor and the loop device in), so the boot path needs
# nothing loaded to reach its root. At boot, the running root is this
# artifact, loop-mounted from the slot and never unpacked. The RAM
# cost of the OS is the page cache, which the kernel reclaims under
# pressure, instead of a permanent tmpfs copy. -all-root makes root
# own everything, no matter who ran the build. -noappend replaces the
# output rather than appending to it on a rebuild.
mksquashfs "$root" "$dist/liken.sqfs" \
    -comp zstd -all-root -noappend -no-progress -quiet

# This builds the boot archive: the one cpio the boot loader still
# stages in RAM. It carries the minimum that must exist before the
# system image is mounted: init itself, the early boot's few modules
# (boot-modules.conf), and mke2fs. The modules live under
# lib/modules/boot with their own depmod index, under a name
# deliberately different from the kernel's release string. This way,
# when init later copies the boot-time files onto the real root,
# nothing here can override the system image's complete index at
# lib/modules/<release>.
#
# mke2fs travels in this archive for the install boot, which runs
# from this archive alone. A machine whose data roles share the boot
# disk (a cloud machine with one disk is the common case) claims and
# formats them during the install, and formatting ext4 is mke2fs's
# job. Ordinary boots never reach this copy of mke2fs: the system
# image carries its own copy at the same path, and the boot archive's
# files never reach the real root.
boot_root="$dist/boot-root"
mkdir -p "$boot_root/lib/modules/$release" "$boot_root/sbin"
cp "$init_dist/liken" "$boot_root/liken"
cp "$here/../e2fsprogs/dist/$e2fsprogs_version/mke2fs" "$boot_root/sbin/mke2fs"

# The boot archive is the one place that still prunes. It stages only
# the few modules on boot-modules.conf, with their dependencies,
# because its size sets the RAM the boot loader stages. The host's
# modprobe resolves each name against the kernel's depmod index
# (--show-depends prints one "insmod <path>" line per file,
# dependencies first), without loading anything anywhere.
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
ship_modules "$boot_root" <"$here/boot-modules.conf"
cp "$kdist/lib/modules/$release/modules.builtin" \
   "$kdist/lib/modules/$release/modules.builtin.modinfo" \
   "$kdist/lib/modules/$release/modules.order" \
   "$boot_root/lib/modules/$release/"
depmod --basedir "$boot_root" "$release"
mv "$boot_root/lib/modules/$release" "$boot_root/lib/modules/boot"

# The list itself travels in the archive too, so init loads exactly
# what the build shipped. One file feeds both decisions.
cp "$here/boot-modules.conf" "$boot_root/lib/modules/boot/boot-modules.conf"

(cd "$boot_root" && find . | cpio --quiet -o -H newc -R +0:+0) >"$dist/boot.cpio"
rm -rf "$boot_root"

echo "image for kernel $release, k3s $k3s_version:"
du -sh "$dist/liken.sqfs" "$dist/boot.cpio"
