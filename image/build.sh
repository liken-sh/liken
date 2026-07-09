#!/usr/bin/env bash
#
# Assemble the liken image: an initramfs the kernel unpacks into RAM.
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
#   /etc/liken/cluster.yaml       what the machines form together: the
#                                 cluster's topology and address plan
#   /etc/liken/machines/          one Machine manifest per machine; a
#                                 boot selects its own by the
#                                 liken.machine=<name> kernel parameter,
#                                 which is how one image boots a fleet.
#                                 The manifests (and cluster.yaml) are
#                                 an *input* to this build, not part of
#                                 this domain: which machines exist and
#                                 what they form is a deployment's
#                                 decision, so the caller points the
#                                 build at a directory carrying them
#                                 (the repo's own lab is dev-cluster/)
#   /etc/liken/token              the cluster's join token, minted
#                                 offline by identity/mint.sh: it
#                                 embeds a hash of the server CA below,
#                                 so a joining machine can verify the
#                                 cluster before presenting the secret
#   /etc/liken/modules.conf       which kernel modules init loads for
#                                 the OS's own needs
#   /lib/modules/<release>/       those modules, any extras the
#                                 deployment's machines declare
#                                 (spec.modules), and everything they
#                                 depend on, exactly as Ubuntu built
#                                 them
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
#   /var/lib/rancher/k3s/         the cluster's certificate authorities,
#     server/tls/                 generated by the identity domain. k3s
#                                 finds them here and signs its leaf
#                                 certs from them instead of inventing
#                                 its own roots, which is what makes an
#                                 operator's kubeconfig computable
#                                 offline (identity/mint.sh). It also
#                                 means the image contains private keys:
#                                 anyone who holds this file controls
#                                 the cluster it boots
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

# The build's one argument says where the Machine and Cluster
# manifests come from. When it is omitted, the image carries no
# manifests at all, and that is still a valid machine: everything
# defaults, with DHCP and a RAM root. The deployment declared nothing,
# so nothing is baked in.
manifests="${1:-}"

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
liken_version="${LIKEN_VERSION:-$(cat "$here/../VERSION")}"
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

# The deployment's manifests, staged at the paths init reads. Copied
# file by file rather than as a tree, so what the image can carry is
# explicit: one cluster document, one manifest per machine.
if [[ -n "$manifests" ]]; then
    if [[ -f "$manifests/cluster.yaml" ]]; then
        cp "$manifests/cluster.yaml" "$root/etc/liken/cluster.yaml"
    fi
    if [[ -d "$manifests/machines" ]]; then
        mkdir -p "$root/etc/liken/machines"
        cp "$manifests"/machines/*.yaml "$root/etc/liken/machines/"
    fi
fi

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

# The pre-generated certificate authorities, placed exactly where k3s
# looks before generating its own. The identity directory is an input
# like the manifests: an identity belongs to a deployment, and the
# caller says which deployment this image is for.
identity="${IDENTITY:?IDENTITY must name the deployment identity directory}"
mkdir -p "$root/var/lib/rancher/k3s/server"
cp -r "$identity/tls" "$root/var/lib/rancher/k3s/server/tls"

# The join token, minted alongside the CAs (identity/mint.sh explains
# the format). It lives under /etc/liken rather than k3s's data
# directory because the clusterState filesystem mounts over the
# latter: a boot input has to sit where no disk will shadow it. Init
# hands k3s the path (token-file), never the value.
cp "$identity/token" "$root/etc/liken/token"
chmod 600 "$root/etc/liken/token"

# The liken API and the programs that operate it, all delivered
# through k3s's own mechanisms: the manifests go where k3s
# auto-applies them, and the OCI images go where k3s auto-imports
# them. The manifests come from four places, by domain: the top-level
# manifests/ carries the cluster-level furniture (the CRDs and the
# liken-system namespace), and each operator and the log relays carry
# their own deployment beside their code. The LIKEN_VERSION
# substitution stamps each manifest with the release it shipped in,
# which is what the pod steward compares against a machine's running
# version.
mkdir -p "$root/var/lib/rancher/k3s/server/manifests"
for manifest in "$here"/../manifests/*.yaml \
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

# The machine's trust store, vendored by the trust domain (where these
# roots come from is explained in trust/fetch.sh). The staged name is
# the conventional path Go's crypto/x509 and most TLS stacks probe.
trust_version="$(cat "$here/../trust/VERSION")"
cp "$here/../trust/dist/$trust_version/cacert.pem" \
   "$root/etc/ssl/certs/ca-certificates.crt"

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
    while IFS= read -r name; do
        [[ -z "$name" || "$name" == \#* ]] && continue
        modprobe -d "$kdist" -S "$release" --show-depends "$name"
    done |
        awk '$1 == "insmod" { print $2 }' |
        sort -u |
        while IFS= read -r file; do
            rel="${file#"$kdist"/lib/modules/"$release"/}"
            mkdir -p "$root/lib/modules/$release/$(dirname "$rel")"
            cp "$file" "$root/lib/modules/$release/$rel"
        done
}
mkdir -p "$root/lib/modules/$release"
ship_modules <"$here/etc/liken/modules.conf"

# The features' kernel halves ship unconditionally too, one list per
# feature (staged above under /etc/liken/features); whether they load
# is the cluster document's call, made at boot, never at build.
ship_modules <"$here/../open-iscsi/modules.conf"

# The deployment's declared modules, read from the same manifests this
# build bakes in. The inventory program parses them with the strict
# parser init uses at boot, so the build and the machine can never
# disagree about what a manifest says. The union is printed here so
# the build log completes the review trail: the OS's list is fixed in
# this repo, the deployment's extras are reviewed in its manifests,
# and this is where the two meet.
if [[ -n "$manifests" ]]; then
    declared="$(go run "$here/inventory" modules "$manifests")"
    if [[ -n "$declared" ]]; then
        echo "modules declared by this deployment's machines:"
        while IFS= read -r name; do echo "  $name"; done <<<"$declared"
        ship_modules <<<"$declared"
    fi
fi

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
(cd "$root" && find . | cpio --quiet -o -H newc -R +0:+0) >"$dist/liken.cpio"

echo "image for kernel $release, k3s $k3s_version:"
du -sh "$dist/liken.cpio"
