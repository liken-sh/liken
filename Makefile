# Every artifact in liken is a plain file with real prerequisites.
# This fact makes GNU Make's model (files, prerequisites, timestamps)
# a natural way to drive the build.
#
# The build gets its inputs in two ways. Most of the system is
# vendored: the build fetches the kernel, k3s, the xtables binaries,
# and the trust store at a pinned version, and verifies each one. The
# Go programs compiled here are liken itself: the init, the two
# operators, and the log relays.
#
# The structure follows the repo's rule to organize by domain. Each
# domain directory (kernel/, k3s/, init/, image/, and so on) has its
# own Makefile. That Makefile owns its rules, and you can run it
# alone with `make -C <domain>`. This root Makefile names the
# artifacts that the domains exchange, and it delegates the work of
# producing them.

# liken's own version stamps the binaries. For a release build, the
# stamp is a release name. For every development build, the stamp is
# the git-described commit. version.mk explains the mechanism, and
# the stamp file that lets Make track the version.
include version.mk

# Version pins name the vendored artifacts. The root Makefile reads
# these pins too, because downstream rules (the image build, QEMU)
# depend on the real files that the pins name, not on phony targets.
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
WPASUPPLICANT_VERSION := $(strip $(file <wpa-supplicant/VERSION))
WPASUPPLICANT_DIST := wpa-supplicant/dist/$(WPASUPPLICANT_VERSION)
SYSTEMDBOOT_VERSION := $(strip $(file <systemd-boot/VERSION))
SYSTEMDBOOT_DIST := systemd-boot/dist/$(SYSTEMDBOOT_VERSION)
GRUB_VERSION := $(strip $(file <grub/VERSION))
GRUB_DIST := grub/dist/$(GRUB_VERSION)
HWDATA_VERSION := $(strip $(file <hwdata/VERSION))
HWDATA_DIST := hwdata/dist/$(HWDATA_VERSION)
TZDATA_VERSION := $(strip $(file <tzdata/VERSION))
TZDATA_DIST := tzdata/dist/$(TZDATA_VERSION)
LINUXFIRMWARE_VERSION := $(strip $(file <linux-firmware/VERSION))
LINUXFIRMWARE_DIST := linux-firmware/dist/$(LINUXFIRMWARE_VERSION)
MICROCODE_VERSION := $(strip $(file <microcode/VERSION))
MICROCODE_DIST := microcode/dist/$(MICROCODE_VERSION)

all: kernel k3s xtables trust e2fsprogs open-iscsi nfs-utils wpa-supplicant systemd-boot grub hwdata tzdata linux-firmware microcode licensing init mount machine-operator cluster-operator logs cli identity image

# The version is part of the artifact's name. So a pin bump changes
# the target path itself, and Make rebuilds the artifact with no
# extra staleness tracking. The prerequisites here match the
# prerequisites that the kernel domain's own Makefile declares.
$(KERNEL_DIST)/vmlinuz: kernel/VERSION kernel/fetch.sh
	$(MAKE) -C kernel

kernel: $(KERNEL_DIST)/vmlinuz

# k3s packages all of Kubernetes as one pinned, verified download.
# See k3s/fetch.sh.
$(K3S_DIST)/k3s: k3s/VERSION k3s/fetch.sh
	$(MAKE) -C k3s

k3s: $(K3S_DIST)/k3s

# The build vendors the netfilter userspace tools from the same
# project that builds k3s's own bundled copy. See xtables/fetch.sh.
$(XTABLES_DIST)/bin/xtables-legacy-multi: xtables/VERSION xtables/fetch.sh
	$(MAKE) -C xtables

xtables: $(XTABLES_DIST)/bin/xtables-legacy-multi

# These are the CA certificates that the machine trusts. The build
# pins them by snapshot date. See trust/fetch.sh.
$(TRUST_DIST)/cacert.pem: trust/VERSION trust/fetch.sh
	$(MAKE) -C trust

trust: $(TRUST_DIST)/cacert.pem

# The PCI naming database is the file that lets the unclaimed-hardware
# report name devices in words. See hwdata/fetch.sh.
$(HWDATA_DIST)/pci.ids: hwdata/VERSION hwdata/fetch.sh
	$(MAKE) -C hwdata

hwdata: $(HWDATA_DIST)/pci.ids

# The timezone database, compiled from IANA's rules so that a CronJob
# can name the zone its schedule means. See tzdata/fetch.sh.
$(TZDATA_DIST)/zoneinfo/UTC: tzdata/VERSION tzdata/DIGESTS tzdata/tz.asc \
		tzdata/fetch.sh
	$(MAKE) -C tzdata

tzdata: $(TZDATA_DIST)/zoneinfo/UTC

# The driver firmware blobs, derived from the kernel's own module
# tree rather than curated. The vmlinuz prerequisite is load-bearing
# twice: the derivation reads the kernel's modules, and a kernel bump
# must re-derive the set. See linux-firmware/fetch.sh and derive.sh.
$(LINUXFIRMWARE_DIST)/derived.txt: linux-firmware/VERSION \
		linux-firmware/fetch.sh linux-firmware/derive.sh \
		$(KERNEL_DIST)/vmlinuz
	$(MAKE) -C linux-firmware

linux-firmware: $(LINUXFIRMWARE_DIST)/derived.txt

# The CPU microcode early cpio. The linux-firmware prerequisite is
# where the AMD families come from; the Intel half has its own pin
# and fetch. See microcode/fetch.sh.
$(MICROCODE_DIST)/microcode.cpio: microcode/VERSION microcode/fetch.sh \
		$(LINUXFIRMWARE_DIST)/derived.txt
	$(MAKE) -C microcode

microcode: $(MICROCODE_DIST)/microcode.cpio

# mke2fs is the program that init runs to make a filesystem on a
# claimed disk. It is a static binary, and the build vendors it. See
# e2fsprogs/fetch.sh.
$(E2FSPROGS_DIST)/mke2fs: e2fsprogs/VERSION e2fsprogs/fetch.sh
	$(MAKE) -C e2fsprogs

e2fsprogs: $(E2FSPROGS_DIST)/mke2fs

# The iSCSI initiator userspace is the host half of the iscsi
# feature: static iscsid and iscsiadm, built from pinned source
# inside a pinned container. This build needs docker or podman on the
# build host, like nfs-utils below. See open-iscsi/fetch.sh. The
# build also produces the OCI image that runs iscsid as the feature's
# DaemonSet.
#
# Two rules match the domain's own rules. The binaries depend only on
# the pin. The image tar also includes the liken version, so only the
# tar goes stale when the version changes.
$(OPENISCSI_DIST)/iscsid $(OPENISCSI_DIST)/iscsiadm &: \
		open-iscsi/VERSION open-iscsi/fetch.sh
	$(MAKE) -C open-iscsi

$(OPENISCSI_DIST)/iscsid-image.tar: $(OPENISCSI_DIST)/iscsid \
		image/oci.sh $(LIKEN_VERSION_STAMP)
	$(MAKE) -C open-iscsi

open-iscsi: $(OPENISCSI_DIST)/iscsid-image.tar

# The NFS client is the host half of the nfs feature: one static
# mount.nfs, built from pinned source inside a pinned container. See
# nfs-utils/fetch.sh. This build has no daemon and no OCI image, on
# purpose, because NFSv4 needs neither.
$(NFSUTILS_DIST)/mount.nfs: nfs-utils/VERSION nfs-utils/fetch.sh
	$(MAKE) -C nfs-utils

nfs-utils: $(NFSUTILS_DIST)/mount.nfs

# The wireless supplicant: one static binary, built from pinned
# source inside a pinned container, the same shape as nfs-utils
# above (wpa-supplicant/fetch.sh). The build configuration is a
# prerequisite because the feature list decides what the binary
# contains.
$(WPASUPPLICANT_DIST)/wpa_supplicant: wpa-supplicant/VERSION \
		wpa-supplicant/build.config wpa-supplicant/fetch.sh
	$(MAKE) -C wpa-supplicant

wpa-supplicant: $(WPASUPPLICANT_DIST)/wpa_supplicant

# systemd-boot is the boot menu on install media. Its fetch.sh
# explains why the stick needs a boot program when installed machines
# do not.
$(SYSTEMDBOOT_DIST)/systemd-bootx64.efi: systemd-boot/VERSION systemd-boot/fetch.sh
	$(MAKE) -C systemd-boot

systemd-boot: $(SYSTEMDBOOT_DIST)/systemd-bootx64.efi

# grub is the bootloader on BIOS machines. Its fetch.sh explains why
# BIOS machines need one when UEFI machines do not. One make run
# produces both boot-sector artifacts: the fetch step delivers
# grub-boot.img, and the build links the core image beside it.
$(GRUB_DIST)/grub-core.img $(GRUB_DIST)/grub-boot.img &: grub/VERSION grub/fetch.sh grub/early.cfg grub/Makefile
	$(MAKE) -C grub

grub: $(GRUB_DIST)/grub-core.img

# Every release bundles third-party notices beside its binaries.
# Several artifacts carry GPL- and LGPL-licensed components, and the
# terms of those licenses require the notices to travel with the
# bytes. See licensing/Makefile, which also owns the
# corresponding-source mirror that the release workflow publishes.
licensing/dist/LICENSES.md: licensing/NOTICES.md licensing/Makefile LICENSE $(wildcard licensing/texts/*.txt) \
		$(MICROCODE_DIST)/microcode.cpio
	$(MAKE) -C licensing

licensing: licensing/dist/LICENSES.md

# The rules that follow build liken's own programs. Each one names
# the shared packages that its binary reads, so a change to a shared
# package rebuilds every binary that reads it. A missing name leaves
# a stale binary that Make treats as current, and the program then
# runs against a version of the API it was never compiled with.
# `go list -deps ./init` is the authority on the real set; check a
# rule against it when the imports change. Each domain's own Makefile
# carries the same list from its side of the directory boundary, so
# change the two together.

# This is liken itself, the Go program that boots as PID 1. See
# init/main.go's header comment. It reads the two document packages
# and the hardware and disk packages, so a change to any of them
# rebuilds it.
init/dist/liken: $(wildcard init/*.go) go.mod go.sum \
		$(wildcard api/*.go) $(wildcard cluster/*.go) \
		$(wildcard machine/*.go) $(wildcard disks/*.go) \
		$(wildcard hardware/*.go) $(LIKEN_VERSION_STAMP)
	$(MAKE) -C init

init: init/dist/liken

# This is mount(8), the one program here that exists because another
# program calls it by name. The kubelet mounts a pod's volumes by
# running `mount`, so the OS has to have one, and mount/main.go's
# header comment explains what the command really does. It shares no
# package with anything else, so its only inputs are its own sources.
mount/dist/mount: $(wildcard mount/*.go) go.mod go.sum
	$(MAKE) -C mount

mount: mount/dist/mount

# The machine operator is the node-local half of operating the OS
# through the cluster's API. The cluster operator is the fleet-level
# half: one pod per machine, versus one pod per fleet. Each main.go's
# header comment explains this difference. The build packages both as
# hand-assembled OCI images, using image/oci.sh, the recipe that
# every in-cluster binary shares. Both share the kubernetes package,
# the raw API client, so both rebuild when that package changes.
machine-operator/dist/liken-machine-operator-image.tar: $(wildcard machine-operator/*.go) \
		go.mod go.sum image/oci.sh \
		$(wildcard api/*.go) $(wildcard cluster/*.go) \
		$(wildcard machine/*.go) $(wildcard kubernetes/*.go) $(wildcard hardware/*.go) \
		$(HWDATA_DIST)/pci.ids $(LIKEN_VERSION_STAMP)
	$(MAKE) -C machine-operator

machine-operator: machine-operator/dist/liken-machine-operator-image.tar

cluster-operator/dist/liken-cluster-operator-image.tar: flux/VERSION flux/fetch.sh \
		$(wildcard cluster-operator/*.go) \
		go.mod go.sum image/oci.sh \
		$(wildcard api/*.go) $(wildcard cluster/*.go) \
		$(wildcard machine/*.go) $(wildcard kubernetes/*.go) $(LIKEN_VERSION_STAMP)
	$(MAKE) -C cluster-operator

cluster-operator: cluster-operator/dist/liken-cluster-operator-image.tar

# The log relays carry the machine's host-level log streams (the
# kernel's, init's, k3s's, and containerd's) into the cluster as pod
# logs. The build packages the relays exactly like the operator: one
# static binary in a hand-assembled OCI image. See logs/main.go and
# image/oci.sh.
logs/dist/liken-logs-image.tar: $(wildcard logs/*.go) \
		go.mod go.sum image/oci.sh \
		$(wildcard api/*.go) $(wildcard machine/*.go) $(LIKEN_VERSION_STAMP)
	$(MAKE) -C logs

logs: logs/dist/liken-logs-image.tar

# The liken CLI is the toolkit that produces and operates deployments.
# See cli/main.go. It runs on the operator's workstation, not inside
# a machine, and public releases ship it. In this repo, the build
# also uses the CLI to mint the dev cluster's identity, below.
cli/dist/liken: $(wildcard cli/*.go) go.mod go.sum \
		$(wildcard api/*.go) $(wildcard cluster/*.go) \
		$(wildcard identity/*.go) $(wildcard machine/*.go) \
		$(wildcard image/*.go) $(wildcard releases/*.go) \
		$(wildcard disks/*.go) $(wildcard scaffold/*.go) \
		$(wildcard scaffold/*.tmpl) $(LIKEN_VERSION_STAMP)
	$(MAKE) -C cli

cli: cli/dist/liken

# The cluster's identity is a set of certificate authorities and the
# join token. The build mints it before any machine boots. See
# identity/mint.go. The token can exist this early because the CA it
# hashes already exists.
#
# An identity belongs to a deployment, not to the OS. So the toolkit
# takes the output directory as an argument, and this root Makefile
# points that argument at the repo's own deployment, the same way the
# image build gets the dev cluster's manifests. The keys are
# gitignored, and the artifacts carry no version. If you lose or
# remake them, the next boot just gets a new identity. Minting keeps
# every artifact that already exists, so an adopted identity (`liken
# adopt`) is not changed by this rule.
IDENTITY_DIR := dev-cluster/identity

# The toolkit is an order-only prerequisite (after the |). Minting
# needs the CLI to exist, but the identity deliberately does not
# depend on the CLI's bytes. A rebuilt toolkit must never make Make
# treat the cluster's identity as stale.
$(IDENTITY_DIR)/tls/server-ca.crt $(IDENTITY_DIR)/token &: | cli/dist/liken
	cli/dist/liken mint $(IDENTITY_DIR)

identity: $(IDENTITY_DIR)/tls/server-ca.crt $(IDENTITY_DIR)/token

# This is an operator's admin credential, computed offline from the
# client CA. The machine never has to provide it. The command reads
# the endpoint from dev-cluster/cluster.yaml by default, but that
# endpoint is the lab's own segment, 10.10.0.1:6443, which the host
# cannot reach. -server points the kubeconfig at 16443 instead, the
# port dev-cluster/Makefile forwards from the founding leader. Name
# this kubeconfig file explicitly in commands, so you never touch a
# kubeconfig you already have:
#
#   kubectl --kubeconfig dev-cluster/identity/kubeconfig get nodes
kubeconfig: $(IDENTITY_DIR)/tls/server-ca.crt cli/dist/liken
	cli/dist/liken kubeconfig -server https://127.0.0.1:16443 dev-cluster

# This is where the OS build meets this repo's own deployment. The
# dev cluster's composed artifacts land beside its manifests and
# identity.
SYSTEM_IMAGE := image/dist/liken.sqfs
BOOT_ARCHIVE := image/dist/boot.cpio
IMAGE_DIR := dev-cluster/image

# The generic system build produces two artifacts from one build:
# liken.sqfs, the whole OS as a read-only filesystem image that init
# mounts as the root, and boot.cpio, the small initramfs that the
# boot loader stages. Neither artifact contains anything about any
# one deployment. They are the OS's own artifacts, so they land in
# the image domain's dist/.
# The prerequisites here match the prerequisites that image/Makefile's
# own rule declares, from its side of the directory boundary. When
# you change either list, change the other list to match.
$(SYSTEM_IMAGE) $(BOOT_ARCHIVE) &: init/dist/liken mount/dist/mount \
		$(KERNEL_DIST)/vmlinuz $(K3S_DIST)/k3s \
		$(XTABLES_DIST)/bin/xtables-legacy-multi \
		$(TRUST_DIST)/cacert.pem \
		$(E2FSPROGS_DIST)/mke2fs \
		$(OPENISCSI_DIST)/iscsid $(OPENISCSI_DIST)/iscsiadm \
		$(OPENISCSI_DIST)/iscsid-image.tar \
		$(wildcard open-iscsi/manifests/*.yaml) \
		open-iscsi/modules.conf open-iscsi/iscsid.conf \
		$(NFSUTILS_DIST)/mount.nfs nfs-utils/modules.conf \
		$(WPASUPPLICANT_DIST)/wpa_supplicant \
		$(wildcard flux/manifests/*.yaml) \
		machine-operator/dist/liken-machine-operator-image.tar \
		$(wildcard machine-operator/manifests/*.yaml) \
		cluster-operator/dist/liken-cluster-operator-image.tar \
		$(wildcard cluster-operator/manifests/*.yaml) \
		$(wildcard machine/manifests/*.yaml) \
		$(wildcard cluster/manifests/*.yaml) \
		$(wildcard kubernetes/manifests/*.yaml) \
		logs/dist/liken-logs-image.tar \
		$(wildcard logs/manifests/*.yaml) \
		image/build.sh image/boot-modules.conf \
		$(HWDATA_DIST)/pci.ids \
		$(TZDATA_DIST)/zoneinfo/UTC \
		$(LINUXFIRMWARE_DIST)/derived.txt \
		$(shell find image/etc -type f) image/Makefile
	$(MAKE) -C image

image: $(SYSTEM_IMAGE) $(BOOT_ARCHIVE)

# The deployment layer is the small archive that turns the generic
# image into the dev cluster's image. It carries the dev cluster's
# manifests and its identity. image/layer.go explains the contents.
$(IMAGE_DIR)/deployment.cpio: cli/dist/liken \
		dev-cluster/cluster.yaml $(wildcard dev-cluster/machines/*.yaml) \
		$(IDENTITY_DIR)/tls/server-ca.crt $(IDENTITY_DIR)/token
	@mkdir -p $(IMAGE_DIR)
	cli/dist/liken layer dev-cluster $(IDENTITY_DIR) $@

# The dev cluster's -kernel boot initrd concatenates four parts: the
# microcode early cpio, which must come first because the kernel
# scans the very start of its initrd for microcode before it
# decompresses anything; the boot archive; the system image, wrapped
# in a bare cpio so the kernel unpacks it into rootfs as a file (init
# loop-mounts it from there, the from-RAM path that rootimage.go
# describes); and the deployment layer.
#
# The kernel's initramfs unpacker processes concatenated cpio
# archives in order into one filesystem. So this build joins the
# parts instead of rebuilding them: the system image is never opened,
# only wrapped and joined. Installed boots (BOOT=disk) do not use
# this file. Their slots carry the same artifacts unwrapped, and
# their boot entries pass each one as its own initrd, in this same
# order.
$(IMAGE_DIR)/initrd.cpio: $(MICROCODE_DIST)/microcode.cpio $(BOOT_ARCHIVE) $(SYSTEM_IMAGE) $(IMAGE_DIR)/deployment.cpio
	cat $(MICROCODE_DIST)/microcode.cpio $(BOOT_ARCHIVE) > $@
	(cd image/dist && echo liken.sqfs | cpio --quiet -o -H newc -R +0:+0) >> $@
	cat $(IMAGE_DIR)/deployment.cpio >> $@

# This target boots the dev cluster's machines. The dev cluster's
# Makefile documents the virtual hardware and every QEMU flag. These
# QEMU guests are used in place of the physical and cloud machines
# that liken really targets.
#
# NODE picks which machine runs in this terminal. To run the whole
# cluster, start one `make run` for the founding leader. Then start a
# `make run NODE=node-N` for each remaining machine, each in its own
# terminal.
#
# A guest reboot resets the VM in place. So the console shows a
# staged spec or a release upgrade applied end to end, with the
# shutdown and the next boot in one continuous log. As with every
# target here, the root Makefile only makes sure the artifacts exist,
# in order, before it hands off to dev-cluster's own Makefile.
run: $(KERNEL_DIST)/vmlinuz $(IMAGE_DIR)/initrd.cpio
	$(MAKE) -C dev-cluster run

# The lab's storage server (dev-cluster/storage) is the guest that
# the iscsi and nfs features are tested against. It runs stock
# Debian, not liken, so it needs no OS artifacts built first. This
# target only exists so you can run everything from the root, for
# convenience.
storage:
	$(MAKE) -C dev-cluster storage

# One-shot boots are for debugging and automation. The liken.oneshot
# flag tells init not to restart k3s. When k3s first exits, the
# machine powers off, QEMU exits, and the console log becomes a
# complete, bounded record of the boot.
run-once: $(KERNEL_DIST)/vmlinuz $(IMAGE_DIR)/initrd.cpio
	$(MAKE) -C dev-cluster run-once

# The smoke drills boot node-1 unattended from blank disks: the
# machine installs itself, boots the installed disk under real
# firmware, and must report Ready over the cluster's API. See
# dev-cluster/smoke.sh. CI runs both drills after building
# everything. The UEFI drill proves the NVRAM boot-entry chain under
# OVMF, and the BIOS drill proves the MBR-and-GRUB chain under
# SeaBIOS. Each drill needs the install image and the admin
# kubeconfig that the readiness poll uses to authenticate.
smoke-uefi: $(KERNEL_DIST)/vmlinuz $(IMAGE_DIR)/install.cpio kubeconfig
	$(MAKE) -C dev-cluster smoke-uefi

smoke-bios: $(KERNEL_DIST)/vmlinuz $(IMAGE_DIR)/install.cpio kubeconfig
	$(MAKE) -C dev-cluster smoke-bios

# The lab's release-shaped channel bundles the current tree the way a
# published release is laid out. A real deployment downloads this
# directory from the release channel. The lab produces its own
# version, so the media targets below assemble it by the exact path a
# deployment would use.
#
# The bundle needs a name in the release grammar. So the lab uses
# today's date with serial 000, a serial that no published release
# ever carries (published releases start at 001). This serial marks
# the bundle as the lab's substitute, and it sorts below any real
# release cut the same day. The build removes yesterday's substitute
# first, so the channel only ever holds one.
LAB_VERSION := $(shell date +%Y.%m.%d)-000

$(IMAGE_DIR)/channel/$(LAB_VERSION)/release.yaml: $(SYSTEM_IMAGE) $(BOOT_ARCHIVE) \
		$(KERNEL_DIST)/vmlinuz $(MICROCODE_DIST)/microcode.cpio \
		cli/dist/liken $(LIKEN_VERSION_STAMP) \
		$(SYSTEMDBOOT_DIST)/systemd-bootx64.efi \
		$(GRUB_DIST)/grub-boot.img $(GRUB_DIST)/grub-core.img \
		licensing/dist/LICENSES.md
	rm -rf $(IMAGE_DIR)/channel
	cli/dist/liken bundle $(KERNEL_DIST)/vmlinuz $(SYSTEM_IMAGE) $(BOOT_ARCHIVE) \
		$(MICROCODE_DIST)/microcode.cpio \
		cli/dist/liken $(SYSTEMDBOOT_DIST)/systemd-bootx64.efi \
		$(GRUB_DIST)/grub-boot.img $(GRUB_DIST)/grub-core.img \
		licensing/dist/LICENSES.md \
		$(IMAGE_DIR)/channel $(LAB_VERSION)

# The install image is for the lab's fast -kernel boots. It composes
# the release with the lab's deployment layer, and it carries the
# payload that the installer verifies and copies onto a machine's own
# disk.
$(IMAGE_DIR)/install.cpio: $(IMAGE_DIR)/channel/$(LAB_VERSION)/release.yaml \
		$(IMAGE_DIR)/deployment.cpio cli/dist/liken
	cli/dist/liken media $(IMAGE_DIR)/channel/$(LAB_VERSION) $(IMAGE_DIR)/deployment.cpio $@

# The hardware-parity deployment (dev-cluster/hardware) boots the dev
# cluster on the shape a real machine has: the disks on an AHCI SATA
# controller and the network cards e1000, the controllers the kernel
# ships as modules. It reuses the dev cluster's cluster document (its
# cluster.yaml is a symlink to the dev cluster's) and the same
# identity, so its node-1 is the same founding leader and the same
# admin kubeconfig authenticates the readiness poll. Only the machine
# manifest differs, so only the layer and the install image differ.
# These rules mirror the dev cluster's own layer and install rules
# above, and share the one release channel. See
# dev-cluster/hardware/README.md.
HW_DIR := dev-cluster/hardware
HW_IMAGE_DIR := dev-cluster/hardware/image

$(HW_IMAGE_DIR)/deployment.cpio: cli/dist/liken \
		$(HW_DIR)/cluster.yaml $(wildcard $(HW_DIR)/machines/*.yaml) \
		$(IDENTITY_DIR)/tls/server-ca.crt $(IDENTITY_DIR)/token
	@mkdir -p $(HW_IMAGE_DIR)
	cli/dist/liken layer $(HW_DIR) $(IDENTITY_DIR) $@

$(HW_IMAGE_DIR)/install.cpio: $(IMAGE_DIR)/channel/$(LAB_VERSION)/release.yaml \
		$(HW_IMAGE_DIR)/deployment.cpio cli/dist/liken
	cli/dist/liken media $(IMAGE_DIR)/channel/$(LAB_VERSION) $(HW_IMAGE_DIR)/deployment.cpio $@

# The parity deployment's install stick, the same composition as the
# dev cluster's stick above. The parity smoke's report step needs a
# real stick: the hardware report writes its proposal onto the stick's
# filesystem, and it finds the stick by the GPT name the stick image
# carries, so no other medium can stand in for it.
$(HW_IMAGE_DIR)/stick.img: $(IMAGE_DIR)/channel/$(LAB_VERSION)/release.yaml \
		$(HW_IMAGE_DIR)/deployment.cpio cli/dist/liken
	cli/dist/liken stick -console ttyS0 $(IMAGE_DIR)/channel/$(LAB_VERSION) $(HW_IMAGE_DIR)/deployment.cpio $@

# The parity smoke walks the whole first-machine flow on the metal
# shape, from blank disks: boot the hardware report and check the
# proposal it writes to the stick, install, boot the installed disk,
# and wait for Ready, the same verdict as smoke-uefi. It proves the
# storage and network module paths a virtio guest never walks. This
# target sits beside the dev cluster's smoke targets in spirit; it
# lives here because it needs the hardware install image and stick
# defined just above.
smoke-hardware: $(KERNEL_DIST)/vmlinuz $(HW_IMAGE_DIR)/install.cpio \
		$(HW_IMAGE_DIR)/stick.img kubeconfig
	$(MAKE) -C dev-cluster smoke-hardware

# The eMMC deployment (dev-cluster/emmc) boots the dev cluster on a
# machine whose only disk is an eMMC card behind an sdhci-pci
# controller. Like the parity deployment above, it reuses the dev
# cluster's cluster document and identity, so its node-1 is the same
# founding leader and the same admin kubeconfig authenticates the
# readiness poll. Only the machine manifest differs, so only the
# layer and the install image differ, and one release channel serves
# all three deployments. It needs no install stick, because its
# drill runs no report boot. dev-cluster/emmc/README.md tells the
# story.
EMMC_DIR := dev-cluster/emmc
EMMC_IMAGE_DIR := dev-cluster/emmc/image

$(EMMC_IMAGE_DIR)/deployment.cpio: cli/dist/liken \
		$(EMMC_DIR)/cluster.yaml $(wildcard $(EMMC_DIR)/machines/*.yaml) \
		$(IDENTITY_DIR)/tls/server-ca.crt $(IDENTITY_DIR)/token
	@mkdir -p $(EMMC_IMAGE_DIR)
	cli/dist/liken layer $(EMMC_DIR) $(IDENTITY_DIR) $@

$(EMMC_IMAGE_DIR)/install.cpio: $(IMAGE_DIR)/channel/$(LAB_VERSION)/release.yaml \
		$(EMMC_IMAGE_DIR)/deployment.cpio cli/dist/liken
	cli/dist/liken media $(IMAGE_DIR)/channel/$(LAB_VERSION) $(EMMC_IMAGE_DIR)/deployment.cpio $@

# The eMMC smoke installs node-1 onto a blank card and boots the
# installed card under OVMF, the same two steps and the same Ready
# verdict as smoke-uefi. It proves the boot archive's eMMC modules,
# the disk walk's skip of the card's boot and RPMB areas, and a
# one-disk machine claiming every storage role on the one disk. It
# is not a CI step: CI's runner carries QEMU 8.2, and the emmc
# device arrived in QEMU 9.1. The target lives here because it needs
# the eMMC install image defined just above.
smoke-emmc: $(KERNEL_DIST)/vmlinuz $(EMMC_IMAGE_DIR)/install.cpio kubeconfig
	$(MAKE) -C dev-cluster smoke-emmc

# The install stick holds the same release and layer as a bootable
# disk image, with the machine menu. This is the medium that real
# hardware uses. The lab sets console=ttyS0 so the menu and every
# installed machine stay on the serial console that QEMU shows.
$(IMAGE_DIR)/stick.img: $(IMAGE_DIR)/channel/$(LAB_VERSION)/release.yaml \
		$(IMAGE_DIR)/deployment.cpio cli/dist/liken
	cli/dist/liken stick -console ttyS0 $(IMAGE_DIR)/channel/$(LAB_VERSION) $(IMAGE_DIR)/deployment.cpio $@

# To install a machine, boot it once from the "USB stick" (the
# install image, loaded via -kernel). The machine writes this release
# onto its own system slots, then powers off. After that,
# `make run NODE=x` boots the machine from that disk.
install: $(IMAGE_DIR)/install.cpio
	$(MAKE) -C dev-cluster install

# This target installs a machine through the real stick, under real
# firmware. OVMF's removable-media path boots systemd-boot's menu on
# the serial console, and a person picks the machine. This drill
# matches what real hardware does. Daily lab installs use the
# unattended target above instead.
install-stick: $(IMAGE_DIR)/stick.img
	$(MAKE) -C dev-cluster install-stick

# The GitOps lab (gitops-cluster/) is the repo's second deployment:
# a one-leader fleet whose declared state lives in a git repository,
# kept for developing the GitOps feature. Its own Makefile explains
# what it is and how it differs from the dev cluster. The rules here
# mirror the dev cluster's composition rules above, against this
# lab's manifests and identity. The release channel bundle is
# shared on purpose: a channel carries no deployment, so the one
# bundle above serves both labs.
GITOPS_IDENTITY_DIR := gitops-cluster/identity
GITOPS_IMAGE_DIR := gitops-cluster/image

$(GITOPS_IDENTITY_DIR)/tls/server-ca.crt $(GITOPS_IDENTITY_DIR)/token &: | cli/dist/liken
	cli/dist/liken mint $(GITOPS_IDENTITY_DIR)

# Same caveat as the dev cluster's kubeconfig target above: the
# endpoint in gitops-cluster/cluster.yaml is the lab's own segment,
# unreachable from the host. This lab's leader forwards 17443, one
# above the dev cluster's 16443, so -server points there instead.
kubeconfig-gitops: $(GITOPS_IDENTITY_DIR)/tls/server-ca.crt cli/dist/liken
	cli/dist/liken kubeconfig -server https://127.0.0.1:17443 gitops-cluster

$(GITOPS_IMAGE_DIR)/deployment.cpio: cli/dist/liken \
		gitops-cluster/cluster.yaml $(wildcard gitops-cluster/machines/*.yaml) \
		$(GITOPS_IDENTITY_DIR)/tls/server-ca.crt $(GITOPS_IDENTITY_DIR)/token
	@mkdir -p $(GITOPS_IMAGE_DIR)
	cli/dist/liken layer gitops-cluster $(GITOPS_IDENTITY_DIR) $@

$(GITOPS_IMAGE_DIR)/initrd.cpio: $(MICROCODE_DIST)/microcode.cpio $(BOOT_ARCHIVE) $(SYSTEM_IMAGE) $(GITOPS_IMAGE_DIR)/deployment.cpio
	cat $(MICROCODE_DIST)/microcode.cpio $(BOOT_ARCHIVE) > $@
	(cd image/dist && echo liken.sqfs | cpio --quiet -o -H newc -R +0:+0) >> $@
	cat $(GITOPS_IMAGE_DIR)/deployment.cpio >> $@

$(GITOPS_IMAGE_DIR)/install.cpio: $(IMAGE_DIR)/channel/$(LAB_VERSION)/release.yaml \
		$(GITOPS_IMAGE_DIR)/deployment.cpio cli/dist/liken
	cli/dist/liken media $(IMAGE_DIR)/channel/$(LAB_VERSION) $(GITOPS_IMAGE_DIR)/deployment.cpio $@

run-gitops: $(KERNEL_DIST)/vmlinuz $(GITOPS_IMAGE_DIR)/initrd.cpio
	$(MAKE) -C gitops-cluster run

install-gitops: $(GITOPS_IMAGE_DIR)/install.cpio
	$(MAKE) -C gitops-cluster install

# This target produces a release: the same system, rebuilt under a
# different version stamp and bundled into releases/dist/ the way a
# release webserver lays it out. The releases Makefile explains the
# shape.
#
# The root Makefile's contribution is the vendored inputs. A release
# rebuilds init, the operators, and the image, but the kernel, k3s,
# and the other vendored artifacts are pinned by their own domains
# and shared by every release. A release contains no deployment and
# no per-deployment channel. The lab's machines upgrade from this
# bundle directly, and they carry their own deployment layer between
# slots.
release: kernel k3s xtables trust e2fsprogs open-iscsi nfs-utils wpa-supplicant hwdata tzdata linux-firmware microcode
	$(MAKE) -C releases release

# This target serves the release channel to the lab over HTTP. The
# guests reach it at http://10.0.2.2:8017, the source URL that the
# dev cluster's Cluster document declares. This is the lab's
# substitute for the public channel at releases.liken.sh.
serve:
	$(MAKE) -C releases serve

# The website: the front page and the manual, built as static files
# (docs/README.md tells the whole story). The site is not an OS
# artifact, so `all` does not build it and a release does not bundle
# it. It ships on its own path: CI deploys the built tree to GitHub
# Pages when a push touches the docs domain.
docs:
	$(MAKE) -C docs

# `make test` runs every check CI runs, in the same commands, so a
# change that passes here passes there.
test: test-go test-docs

# The gate measures coverage on its own run, on its own pinned
# toolchain, and the pin is the point.
#
# Go 1.27 splits a basic block into one profile row per run of code,
# so a comment inside a function ends that row and starts another.
# Each row repeats the whole block's statement count, and every
# reader sums the rows, `go tool cover` included. A block written as
# three runs of code therefore counts three times. The error is not a
# constant: it grows with the comments inside a function, so it moves
# a package's number up or down when someone only reflows a comment.
# On this repo it moves machine-operator down 11.6 points and disks
# up 5.9.
#
# Go 1.26 counts each block once, so the pin below measures what
# .testcoverage.yml's thresholds were set against. Move it to the
# newest toolchain that counts each block once.
#
# go-test-coverage is a pinned tool dependency (the `tool` directive
# in go.mod), so the gate needs nothing installed beyond the Go
# toolchain. Its thresholds live in .testcoverage.yml, which explains
# how they are chosen. The profile itself is gitignored.
COVERAGE_TOOLCHAIN := go1.26.7

# The repo's programs are one Go module (see go.mod at the root). So
# every check is a single invocation of ./.... This covers Go files in
# any domain directory, present or future, without listing them. The
# one exception is docs/, a nested module that exists to keep Hugo's
# dependency graph out of the root go.sum; ./... does not descend into
# it, and test-docs below runs the same checks against that module.
#
# gofmt is not here. It stays its own pre-commit hook, because that
# hook rewrites the files it finds with the toolchain's own gofmt,
# and a make target could only report them.
test-go:
	go vet ./...
	go tool staticcheck ./...
	go test ./...
	GOTOOLCHAIN=$(COVERAGE_TOOLCHAIN) go test -coverprofile=coverage.out ./...
	GOTOOLCHAIN=$(COVERAGE_TOOLCHAIN) go tool go-test-coverage --config=.testcoverage.yml

test-docs:
	$(MAKE) -C docs test
	$(MAKE) -C docs build

# Every domain that vendors something says what it pins and what its
# upstream has now. The domains are listed in build order, so the
# table reads in the order a release is assembled, and the last two
# are the pins that belong to no build: the source mirror's own, and
# the lab's storage guest.
#
# Each latest.sh prints tab-separated rows and no formatting, so the
# rule adds the verdict and the alignment here, once, instead of
# seventeen times. A domain that pins more than one thing prints a row
# for each, and indents the name of a pin that hangs off another,
# because which pin owns which is the domain's own knowledge.
#
# This target reaches the network, so it is not part of any build.
VERSION_DOMAINS := kernel k3s xtables trust e2fsprogs open-iscsi \
	nfs-utils wpa-supplicant systemd-boot grub hwdata tzdata linux-firmware \
	microcode flux docs licensing dev-cluster/storage

versions:
	@{ printf 'COMPONENT\tPINNED\tLATEST\tSTATE\tNOTE\n'; \
	   for domain in $(VERSION_DOMAINS); do \
	       $$domain/latest.sh | awk -F'\t' -v OFS='\t' \
	           '{ state = ($$3 == "?") ? "unknown" : ($$2 == $$3 ? "current" : "behind"); \
	              print $$1, $$2, $$3, state, $$4 }'; \
	   done; } | column -t -s "$$(printf '\t')"

# Cleaning includes the dev cluster's disks. If the build removed
# every domain's artifacts but left the machine state behind, the
# next boot would still carry the old cluster's state. That would not
# be a clean boot.
clean:
	$(MAKE) -C docs clean
	$(MAKE) -C releases clean
	$(MAKE) -C dev-cluster clean
	$(MAKE) -C gitops-cluster clean
	$(MAKE) -C kernel clean
	$(MAKE) -C k3s clean
	$(MAKE) -C xtables clean
	$(MAKE) -C trust clean
	$(MAKE) -C e2fsprogs clean
	$(MAKE) -C open-iscsi clean
	$(MAKE) -C nfs-utils clean
	$(MAKE) -C wpa-supplicant clean
	$(MAKE) -C systemd-boot clean
	$(MAKE) -C grub clean
	$(MAKE) -C licensing clean
	$(MAKE) -C init clean
	$(MAKE) -C mount clean
	$(MAKE) -C machine-operator clean
	$(MAKE) -C cluster-operator clean
	$(MAKE) -C logs clean
	$(MAKE) -C cli clean
	rm -rf $(IDENTITY_DIR)
	$(MAKE) -C image clean
	rm -rf $(IMAGE_DIR)
	rm -rf $(HW_IMAGE_DIR)
	rm -rf $(EMMC_IMAGE_DIR)

.PHONY: versions all kernel k3s xtables trust e2fsprogs open-iscsi nfs-utils wpa-supplicant systemd-boot grub hwdata tzdata linux-firmware microcode licensing init mount machine-operator cluster-operator logs cli identity kubeconfig kubeconfig-gitops image run run-once run-gitops smoke-uefi smoke-bios smoke-hardware smoke-emmc install install-stick install-gitops storage release serve docs test test-go test-docs clean
