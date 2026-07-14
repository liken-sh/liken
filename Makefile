# Every artifact in liken is a plain file with real prerequisites,
# which makes GNU Make's model (files, prerequisites, timestamps)
# the natural way to drive the build. The inputs come two ways. Most
# of the system is vendored: the kernel, k3s, the xtables binaries,
# and the trust store are each fetched at a pinned version and
# verified. The parts that are liken itself, the init, the two
# operators, and the log relays, are the Go programs compiled here.
#
# The structure follows the repo's rule of organizing by domain: each
# domain directory (kernel/, k3s/, init/, image/, ...) has its own
# Makefile that owns its rules and can be run standalone with
# `make -C <domain>`. This root Makefile names the artifacts the domains
# exchange and delegates the work of producing them.

# liken's own version stamps the binaries: a release name when the
# releases domain is building, the git-described commit for every
# development build (version.mk explains the mechanism and the stamp
# file that lets Make track it).
include version.mk

# Version pins name the vendored artifacts, so the root reads them too:
# downstream rules (the image build, QEMU) depend on these real files,
# not on phony targets.
KERNEL_VERSION := $(strip $(file <kernel/VERSION))
KERNEL_DIST := kernel/dist/$(KERNEL_VERSION)
K3S_VERSION := $(strip $(file <k3s/VERSION))
K3S_DIST := k3s/dist/$(K3S_VERSION)
XTABLES_VERSION := $(strip $(file <xtables/VERSION))
XTABLES_DIST := xtables/dist/$(XTABLES_VERSION)
TRUST_VERSION := $(strip $(file <trust/VERSION))
TRUST_DIST := trust/dist/$(TRUST_VERSION)
E2FSPROGS_VERSION := $(strip $(file <e2fsprogs/VERSION))
E2FSPROGS_DIST := e2fsprogs/dist/$(E2FSPROGS_VERSION)
OPENISCSI_VERSION := $(strip $(file <open-iscsi/VERSION))
OPENISCSI_DIST := open-iscsi/dist/$(OPENISCSI_VERSION)
NFSUTILS_VERSION := $(strip $(file <nfs-utils/VERSION))
NFSUTILS_DIST := nfs-utils/dist/$(NFSUTILS_VERSION)
SYSTEMDBOOT_VERSION := $(strip $(file <systemd-boot/VERSION))
SYSTEMDBOOT_DIST := systemd-boot/dist/$(SYSTEMDBOOT_VERSION)
GRUB_VERSION := $(strip $(file <grub/VERSION))
GRUB_DIST := grub/dist/$(GRUB_VERSION)

all: kernel k3s xtables trust e2fsprogs open-iscsi nfs-utils systemd-boot grub init machine-operator cluster-operator logs cli identity image

# Because the version is part of the artifact's name, a pin bump
# changes the target path itself and Make rebuilds with no extra
# staleness tracking. The prerequisites here mirror the ones the
# kernel domain's own Makefile declares.
$(KERNEL_DIST)/vmlinuz: kernel/VERSION kernel/fetch.sh
	$(MAKE) -C kernel

kernel: $(KERNEL_DIST)/vmlinuz

# k3s packages all of Kubernetes as one pinned, verified download
# (see k3s/fetch.sh).
$(K3S_DIST)/k3s: k3s/VERSION k3s/fetch.sh
	$(MAKE) -C k3s

k3s: $(K3S_DIST)/k3s

# The netfilter userspace tools are vendored from the same project
# that builds k3s's own bundled copy (see xtables/fetch.sh).
$(XTABLES_DIST)/bin/xtables-legacy-multi: xtables/VERSION xtables/fetch.sh
	$(MAKE) -C xtables

xtables: $(XTABLES_DIST)/bin/xtables-legacy-multi

# These are the CA certificates the machine trusts, pinned by
# snapshot date (see trust/fetch.sh).
$(TRUST_DIST)/cacert.pem: trust/VERSION trust/fetch.sh
	$(MAKE) -C trust

trust: $(TRUST_DIST)/cacert.pem

# mke2fs is the program init execs to make a filesystem on a claimed
# disk. It is a static binary, vendored (see e2fsprogs/fetch.sh).
$(E2FSPROGS_DIST)/mke2fs: e2fsprogs/VERSION e2fsprogs/fetch.sh
	$(MAKE) -C e2fsprogs

e2fsprogs: $(E2FSPROGS_DIST)/mke2fs

# The iSCSI initiator userspace, the host half of the iscsi feature:
# static iscsid and iscsiadm built from pinned source inside a pinned
# container, which needs docker or podman on the build host, like
# nfs-utils below (see open-iscsi/fetch.sh), plus the OCI image that
# runs iscsid as the feature's DaemonSet. Two rules, mirroring the
# domain's own: the binaries are a function of the pin alone, while
# the image tar also bakes the liken version, so only the tar goes
# stale when the version moves.
$(OPENISCSI_DIST)/iscsid $(OPENISCSI_DIST)/iscsiadm &: \
		open-iscsi/VERSION open-iscsi/fetch.sh
	$(MAKE) -C open-iscsi

$(OPENISCSI_DIST)/iscsid-image.tar: $(OPENISCSI_DIST)/iscsid \
		image/oci.sh $(LIKEN_VERSION_STAMP)
	$(MAKE) -C open-iscsi

open-iscsi: $(OPENISCSI_DIST)/iscsid-image.tar

# The NFS client, the host half of the nfs feature: one static
# mount.nfs, built from pinned source inside a pinned container (see
# nfs-utils/fetch.sh). No daemon and no OCI image, deliberately;
# NFSv4 needs neither.
$(NFSUTILS_DIST)/mount.nfs: nfs-utils/VERSION nfs-utils/fetch.sh
	$(MAKE) -C nfs-utils

nfs-utils: $(NFSUTILS_DIST)/mount.nfs

# systemd-boot: the boot menu on install media (its fetch.sh explains
# why the stick needs a boot program when installed machines don't).
$(SYSTEMDBOOT_DIST)/systemd-bootx64.efi: systemd-boot/VERSION systemd-boot/fetch.sh
	$(MAKE) -C systemd-boot

systemd-boot: $(SYSTEMDBOOT_DIST)/systemd-bootx64.efi

# grub: the bootloader on BIOS machines (its fetch.sh explains why
# BIOS machines need one when UEFI machines don't). Both boot-sector
# artifacts come from one make: the fetch delivers grub-boot.img and
# the core image is linked beside it.
$(GRUB_DIST)/grub-core.img $(GRUB_DIST)/grub-boot.img &: grub/VERSION grub/fetch.sh grub/early.cfg grub/Makefile
	$(MAKE) -C grub

grub: $(GRUB_DIST)/grub-core.img

# This is liken itself, the Go program that boots as PID 1 (see
# init/main.go's header comment). It shares the machine package (the
# Machine API as Go types) with the operator, so both rebuild when
# that package changes.
init/dist/liken: $(wildcard init/*.go) go.mod go.sum \
		$(wildcard machine/*.go) $(wildcard disks/*.go) $(LIKEN_VERSION_STAMP)
	$(MAKE) -C init

init: init/dist/liken

# The machine operator is the node-local half of operating the OS
# through the cluster's API, and the cluster operator is the
# fleet-level half: one pod per machine versus one pod per fleet (each
# main.go's header comment draws the line). Both are packaged as
# hand-assembled OCI images (image/oci.sh, the recipe every
# in-cluster binary shares) and both share the kubernetes package,
# the raw API client, so both rebuild when it changes.
machine-operator/dist/liken-machine-operator-image.tar: $(wildcard machine-operator/*.go) \
		go.mod go.sum image/oci.sh \
		$(wildcard machine/*.go) $(wildcard kubernetes/*.go) $(LIKEN_VERSION_STAMP)
	$(MAKE) -C machine-operator

machine-operator: machine-operator/dist/liken-machine-operator-image.tar

cluster-operator/dist/liken-cluster-operator-image.tar: $(wildcard cluster-operator/*.go) \
		go.mod go.sum image/oci.sh \
		$(wildcard machine/*.go) $(wildcard kubernetes/*.go) $(LIKEN_VERSION_STAMP)
	$(MAKE) -C cluster-operator

cluster-operator: cluster-operator/dist/liken-cluster-operator-image.tar

# The log relays carry the machine's host-level log streams (the
# kernel's, init's, k3s's, containerd's) into the cluster as pod
# logs. Packaged exactly like the operator: one static binary in a
# hand-assembled OCI image (see logs/main.go and image/oci.sh).
logs/dist/liken-logs-image.tar: $(wildcard logs/*.go) \
		go.mod go.sum image/oci.sh \
		$(wildcard machine/*.go) $(LIKEN_VERSION_STAMP)
	$(MAKE) -C logs

logs: logs/dist/liken-logs-image.tar

# The liken CLI: the toolkit that produces and operates deployments
# (see cli/main.go). It runs on the operator's workstation, not
# inside a machine, and it ships with public releases; in this repo
# it is also how the build itself mints the dev cluster's identity
# below.
cli/dist/liken: $(wildcard cli/*.go) go.mod go.sum \
		$(wildcard identity/*.go) $(wildcard machine/*.go) \
		$(wildcard image/*.go) $(wildcard releases/*.go) \
		$(wildcard disks/*.go) $(wildcard scaffold/*.go) \
		$(wildcard scaffold/*.tmpl) $(LIKEN_VERSION_STAMP)
	$(MAKE) -C cli

cli: cli/dist/liken

# The cluster's identity is a set of certificate authorities and the
# join token, minted before any machine boots (see identity/mint.go).
# The token can exist this early because the CA it hashes already
# exists. An identity belongs to a deployment, not to the OS, so the
# toolkit takes the output directory as an argument, and this root
# Makefile points it at the repo's own deployment, the same way the
# image build gets the dev cluster's manifests. The keys are
# gitignored and the artifacts carry no version; losing or remaking
# them just gives the next boot a new identity. Minting keeps every
# artifact that already exists, so an adopted identity (`liken
# adopt`) survives this rule untouched.
IDENTITY_DIR := dev-cluster/identity

# The toolkit is an order-only prerequisite (after the |): minting
# needs the CLI to exist, but the identity is deliberately not a
# function of the CLI's bytes — a rebuilt toolkit must never make
# Make think the cluster's identity is stale.
$(IDENTITY_DIR)/tls/server-ca.crt $(IDENTITY_DIR)/token &: | cli/dist/liken
	cli/dist/liken mint $(IDENTITY_DIR)

identity: $(IDENTITY_DIR)/tls/server-ca.crt $(IDENTITY_DIR)/token

# This is an operator's admin credential, computed offline from the
# client CA; the machine never has to provide it (see
# identity/kubeconfig.go). Use it explicitly, so no kubeconfig you
# already have is ever touched:
#
#   kubectl --kubeconfig dev-cluster/identity/kubeconfig get nodes
kubeconfig: $(IDENTITY_DIR)/tls/server-ca.crt cli/dist/liken
	cli/dist/liken kubeconfig $(IDENTITY_DIR)

# Where the OS build meets this repo's own deployment: the dev
# cluster's composed artifacts land beside its manifests and identity.
SYSTEM_IMAGE := image/dist/liken.sqfs
BOOT_ARCHIVE := image/dist/boot.cpio
IMAGE_DIR := dev-cluster/image

# The generic system, two artifacts from one build: liken.sqfs, the
# whole OS as a read-only filesystem image init mounts as the root,
# and boot.cpio, the small initramfs the boot loader stages — with
# nothing about any one deployment inside either. They are the OS's
# own artifacts, so they land in the image domain's dist/.
# The prerequisites here mirror the ones image/Makefile's own rule
# declares (from its side of the directory boundary); when either
# list changes, change the other to match.
$(SYSTEM_IMAGE) $(BOOT_ARCHIVE) &: init/dist/liken $(KERNEL_DIST)/vmlinuz $(K3S_DIST)/k3s \
		$(XTABLES_DIST)/bin/xtables-legacy-multi \
		$(TRUST_DIST)/cacert.pem \
		$(E2FSPROGS_DIST)/mke2fs \
		$(OPENISCSI_DIST)/iscsid $(OPENISCSI_DIST)/iscsiadm \
		$(OPENISCSI_DIST)/iscsid-image.tar \
		$(wildcard open-iscsi/manifests/*.yaml) \
		open-iscsi/modules.conf open-iscsi/iscsid.conf \
		$(NFSUTILS_DIST)/mount.nfs nfs-utils/modules.conf \
		machine-operator/dist/liken-machine-operator-image.tar \
		$(wildcard machine-operator/manifests/*.yaml) \
		cluster-operator/dist/liken-cluster-operator-image.tar \
		$(wildcard cluster-operator/manifests/*.yaml) \
		$(wildcard manifests/*.yaml) \
		logs/dist/liken-logs-image.tar \
		$(wildcard logs/manifests/*.yaml) \
		image/build.sh image/boot-modules.conf \
		$(shell find image/etc -type f) image/Makefile
	$(MAKE) -C image

image: $(SYSTEM_IMAGE) $(BOOT_ARCHIVE)

# The deployment layer: the small archive that makes the generic
# image the dev cluster's image — its manifests, its identity, and
# its machines' declared kernel modules (image/layer.go explains the
# contents; the kernel dist is where the module files and depmod
# index come from).
$(IMAGE_DIR)/deployment.cpio: cli/dist/liken \
		dev-cluster/cluster.yaml $(wildcard dev-cluster/machines/*.yaml) \
		$(IDENTITY_DIR)/tls/server-ca.crt $(IDENTITY_DIR)/token \
		$(KERNEL_DIST)/vmlinuz
	@mkdir -p $(IMAGE_DIR)
	cli/dist/liken layer dev-cluster $(IDENTITY_DIR) $(KERNEL_DIST) $@

# The dev cluster's -kernel boot initrd: the boot archive, the system
# image wrapped in a bare cpio so the kernel unpacks it into rootfs as
# a file (init loop-mounts it from there — the from-RAM path
# rootimage.go describes), and the deployment layer, concatenated.
# The kernel's initramfs unpacker processes concatenated cpio archives
# in order into one filesystem, so composition replaces rebuilding:
# the system image is never opened, only wrapped and joined. Installed
# boots (BOOT=disk) don't use this file; their slots carry the same
# artifacts unwrapped.
$(IMAGE_DIR)/initrd.cpio: $(BOOT_ARCHIVE) $(SYSTEM_IMAGE) $(IMAGE_DIR)/deployment.cpio
	cat $(BOOT_ARCHIVE) > $@
	(cd image/dist && echo liken.sqfs | cpio --quiet -o -H newc -R +0:+0) >> $@
	cat $(IMAGE_DIR)/deployment.cpio >> $@

# Boot the dev cluster's machines, the QEMU guests that stand in for
# the physical and cloud machines liken really targets (the virtual
# hardware and every QEMU flag are documented in dev-cluster/
# Makefile). NODE picks which machine runs in this terminal. To run
# the whole cluster, start one `make run` for the founding leader,
# then a `make run NODE=node-N` for each remaining machine, each in
# its own terminal. A guest reboot resets the VM in place, so the
# console shows a staged spec or a release upgrade applied end to
# end, with the shutdown and the next boot in one stream. As with
# every target here, the root Makefile only makes sure the artifacts
# exist, in order, before handing off.
run: $(KERNEL_DIST)/vmlinuz $(IMAGE_DIR)/initrd.cpio
	$(MAKE) -C dev-cluster run

# The lab's storage server (dev-cluster/storage): the guest the
# iscsi and nfs features are drilled against. It runs stock Debian,
# not liken, so it needs no OS artifacts built first — the delegation
# is pure convenience for running everything from the root.
storage:
	$(MAKE) -C dev-cluster storage

# One-shot boots are for debugging and automation. The liken.oneshot
# flag tells init not to restart k3s: when k3s first exits, the
# machine powers off, QEMU exits, and the console log is a complete,
# bounded record of the boot.
run-once: $(KERNEL_DIST)/vmlinuz $(IMAGE_DIR)/initrd.cpio
	$(MAKE) -C dev-cluster run-once

# The smoke drill: boot node-1 unattended from blank disks and pass
# when its node reports Ready over the cluster's API (see
# dev-cluster/smoke.sh). CI runs this after building everything; it
# needs the same artifacts as `run` plus the admin kubeconfig the
# readiness poll authenticates with.
smoke: $(KERNEL_DIST)/vmlinuz $(IMAGE_DIR)/initrd.cpio kubeconfig
	$(MAKE) -C dev-cluster smoke

# The lab's release-shaped channel: the current tree bundled the way
# a published release is laid out. A real deployment downloads this
# directory from the release channel; the lab produces its own, so
# the media targets below assemble by exactly the path a deployment
# would use. The bundle needs a name in the release grammar, so the
# lab uses today's date with serial 000 — a serial no published
# release ever carries (they start at 001), which both marks this as
# the lab's stand-in and sorts it below any real release cut the same
# day. Yesterday's stand-in is swept first so the channel only ever
# holds one.
LAB_VERSION := $(shell date +%Y.%m.%d)-000

$(IMAGE_DIR)/channel/$(LAB_VERSION)/release.yaml: $(SYSTEM_IMAGE) $(BOOT_ARCHIVE) \
		$(KERNEL_DIST)/vmlinuz cli/dist/liken $(LIKEN_VERSION_STAMP) \
		$(SYSTEMDBOOT_DIST)/systemd-bootx64.efi \
		$(GRUB_DIST)/grub-boot.img $(GRUB_DIST)/grub-core.img
	rm -rf $(IMAGE_DIR)/channel
	cli/dist/liken bundle $(KERNEL_DIST)/vmlinuz $(SYSTEM_IMAGE) $(BOOT_ARCHIVE) \
		cli/dist/liken $(SYSTEMDBOOT_DIST)/systemd-bootx64.efi \
		$(GRUB_DIST)/grub-boot.img $(GRUB_DIST)/grub-core.img \
		$(IMAGE_DIR)/channel $(LAB_VERSION)

# The install image for the lab's fast -kernel boots: the release
# composed with the lab's deployment layer, carrying the payload the
# installer verifies and copies onto a machine's own disk.
$(IMAGE_DIR)/install.cpio: $(IMAGE_DIR)/channel/$(LAB_VERSION)/release.yaml \
		$(IMAGE_DIR)/deployment.cpio cli/dist/liken
	cli/dist/liken media $(IMAGE_DIR)/channel/$(LAB_VERSION) $(IMAGE_DIR)/deployment.cpio $@

# The install stick: the same release and layer as a bootable disk
# image with the machine menu, the medium real hardware uses. The lab
# bakes console=ttyS0 so the menu and every installed machine stay on
# the serial console QEMU shows us.
$(IMAGE_DIR)/stick.img: $(IMAGE_DIR)/channel/$(LAB_VERSION)/release.yaml \
		$(IMAGE_DIR)/deployment.cpio cli/dist/liken
	cli/dist/liken stick -console ttyS0 $(IMAGE_DIR)/channel/$(LAB_VERSION) $(IMAGE_DIR)/deployment.cpio $@

# Install a machine: boot it once from the "USB stick" (the install
# image via -kernel), let it put this release on its own system
# slots, and power off. After that, `make run NODE=x` boots it from
# that disk.
install: $(IMAGE_DIR)/install.cpio
	$(MAKE) -C dev-cluster install

# Install a machine through the real stick under real firmware:
# OVMF's removable-media path boots systemd-boot's menu on the serial
# console, and a person picks the machine. This is the drill for what
# hardware does; daily lab installs keep the unattended target above.
install-stick: $(IMAGE_DIR)/stick.img
	$(MAKE) -C dev-cluster install-stick

# Produce a release: the same system rebuilt under a different
# version stamp and bundled into releases/dist/ the way a release
# webserver lays it out (the releases Makefile explains the shape).
# The root's contribution is the vendored inputs: a release rebuilds
# init, the operators, and the image, but the kernel, k3s, and the
# other vendored artifacts are pinned by their own domains and shared
# by every release. There is no deployment in a release and no
# per-deployment channel: the lab's machines upgrade from this bundle
# directly, carrying their own deployment layer between slots.
release: kernel k3s xtables trust e2fsprogs open-iscsi nfs-utils
	$(MAKE) -C releases release

# Serve the release channel to the lab over HTTP; the guests reach
# this at http://10.0.2.2:8017, the source URL the dev cluster's
# Cluster document declares. This is the lab's stand-in for the
# releases on the liken.sh website.
serve:
	$(MAKE) -C releases serve

# Cleaning includes the dev cluster's disks. If every domain's
# artifacts were removed but the machine state stayed behind, the
# next boot would still carry the old cluster's state, and that would
# not be a clean boot.
clean:
	$(MAKE) -C releases clean
	$(MAKE) -C dev-cluster clean
	$(MAKE) -C kernel clean
	$(MAKE) -C k3s clean
	$(MAKE) -C xtables clean
	$(MAKE) -C trust clean
	$(MAKE) -C e2fsprogs clean
	$(MAKE) -C open-iscsi clean
	$(MAKE) -C nfs-utils clean
	$(MAKE) -C systemd-boot clean
	$(MAKE) -C grub clean
	$(MAKE) -C init clean
	$(MAKE) -C machine-operator clean
	$(MAKE) -C cluster-operator clean
	$(MAKE) -C logs clean
	$(MAKE) -C cli clean
	rm -rf $(IDENTITY_DIR)
	$(MAKE) -C image clean
	rm -rf $(IMAGE_DIR)

.PHONY: all kernel k3s xtables trust e2fsprogs open-iscsi nfs-utils systemd-boot grub init machine-operator cluster-operator logs cli identity kubeconfig image run run-once smoke install install-stick storage release serve clean
