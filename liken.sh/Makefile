# Building the liken.sh deployment: the artifacts that turn a Linode
# instance into this cluster's founding leader.
#
# The shape mirrors the dev cluster's: mint an identity, pack the
# deployment layer, compose install media from a release channel.
# What's different is the destination. The dev cluster boots QEMU
# guests directly; this deployment produces a complete installed
# *disk image*, because the machine it targets is a Linode whose
# disk we can write but whose console we only watch. The install
# still happens the ordinary way — the installer boots and runs
# exactly as it would on hardware — but it runs here, in a local
# QEMU guest under real UEFI firmware, against a raw disk file. The
# written disk is then copied byte-for-byte onto the machine's
# system disk, where it boots BIOS-style through the grub.cfg this
# Makefile plants on the system slot (grub.cfg explains that
# bridge).
#
# The install must run under UEFI even though Linode will boot the
# result under BIOS: the installer records its boot entries in
# firmware variables, and refuses a firmware that has none. The
# variables it writes live in the local OVMF_VARS.fd and are
# discarded with it — the disk carries everything else.
#
# This Makefile assumes the repo's root build is current
# (`make -C ..`): it composes the generic image, the CLI, and the
# vendored kernel; it never rebuilds them.

KERNEL_VERSION := $(strip $(file <../kernel/VERSION))
KERNEL_DIST := ../kernel/dist/$(KERNEL_VERSION)
VMLINUZ := $(KERNEL_DIST)/vmlinuz
SYSTEMDBOOT_VERSION := $(strip $(file <../systemd-boot/VERSION))
SDBOOT := ../systemd-boot/dist/$(SYSTEMDBOOT_VERSION)/systemd-bootx64.efi
GRUB_VERSION := $(strip $(file <../grub/VERSION))
GRUB_BOOT := ../grub/dist/$(GRUB_VERSION)/grub-boot.img
GRUB_CORE := ../grub/dist/$(GRUB_VERSION)/grub-core.img
CLI := ../cli/dist/liken
SYSTEM_IMAGE := ../image/dist/liken.sqfs
BOOT_ARCHIVE := ../image/dist/boot.cpio

# The stand-in version for locally assembled media, exactly the root
# Makefile's convention: today's date with serial 000, which no
# published release ever carries. When CI publishes real releases,
# install media gets built from a downloaded channel instead.
VERSION := $(shell date +%Y.%m.%d)-000

# The system image's size: 3 GiB holds both boot slots, the machine's
# state, and its scratch space, with room left over. Keeping it small
# keeps the system disk disposable — everything durable lives on the
# machine's data disk, which is claimed once and never reinstalled.
# The image may land on a disk slightly larger than itself (cloud
# providers' advertised sizes don't land on exact byte counts): a GPT
# records where its disk ends, and liken notices a disk larger than
# its table claims on first boot and rewrites the table to the real
# end.
SYSTEM_DISK_BYTES := 3221225472

# A scratch stand-in for the machine's data disk, present during the
# local install only so that storage reconciliation finds every
# declared device. Its contents never travel: the real data disk
# starts blank on Linode and the machine claims it on first boot.
DATA_SCRATCH_BYTES := 23622320128

all: image/disk.img

# The cluster's identity: certificate authorities and the join
# token, minted before any machine boots. The CLI is an order-only
# prerequisite for the same reason as at the root: a rebuilt toolkit
# must never make Make think this cluster's identity is stale.
identity/tls/server-ca.crt identity/token &: | $(CLI)
	$(CLI) mint identity

identity: identity/tls/server-ca.crt identity/token

# An operator's admin credential, computed offline from the client
# CA. The endpoint inside points at the cluster segment; reaching it
# from outside means overriding the server, which the drills do with
# kubectl's --server and --tls-server-name flags.
kubeconfig: identity/tls/server-ca.crt $(CLI)
	$(CLI) kubeconfig identity

# The deployment layer: this cluster's manifests, identity, and its
# machines' declared kernel modules, packed as the second cpio
# archive every boot loads over the generic OS.
image/deployment.cpio: $(CLI) cluster.yaml $(wildcard machines/*.yaml) \
		identity/tls/server-ca.crt identity/token $(VMLINUZ)
	@mkdir -p image
	$(CLI) layer . identity $(KERNEL_DIST) $@

# The release-shaped channel this install draws from: the current
# tree bundled the way a published release is laid out.
image/channel/$(VERSION)/release.yaml: $(SYSTEM_IMAGE) $(BOOT_ARCHIVE) $(VMLINUZ) $(CLI) $(SDBOOT) $(GRUB_BOOT) $(GRUB_CORE)
	rm -rf image/channel
	$(CLI) bundle $(VMLINUZ) $(SYSTEM_IMAGE) $(BOOT_ARCHIVE) $(CLI) $(SDBOOT) $(GRUB_BOOT) $(GRUB_CORE) image/channel $(VERSION)

# The install image: the release composed with this deployment's
# layer, carrying the payload the installer verifies and copies onto
# the machine's disk.
image/install.cpio: image/channel/$(VERSION)/release.yaml image/deployment.cpio $(CLI)
	$(CLI) media image/channel/$(VERSION) image/deployment.cpio $@

# A fresh variable store for the local install's firmware. Blank on
# purpose, and disposable: the boot entries the installer writes
# into it are UEFI furniture the BIOS Linode can't read anyway.
image/OVMF_VARS.fd:
	@mkdir -p image
	cp /usr/share/OVMF/OVMF_VARS_4M.fd $@

# The installed disk, produced by really installing. QEMU boots the
# installer under OVMF with the raw image attached as a virtio-scsi
# disk — Linode's controller, so the disk claims itself at the
# /dev/sda the Machine manifest names. The installer partitions,
# copies the release into slot A, writes its (local, discarded) boot
# entries, and powers off; -no-reboot turns that power-off into
# QEMU's exit. The console log lands next to the image for reading
# afterward.
#
# Then the BIOS bridge. Linode's hosts boot BIOS-style only, and a
# liken machine normally has no bootloader at all (UEFI boot entries
# do that job), so this disk carries a complete GRUB installation:
# a BIOS boot partition tucked into the alignment gap before the
# first real partition (sectors 34-2047, touching nothing the
# installer laid out), GRUB's first stage in the MBR's boot code,
# and the /boot/grub tree — config and modules — on slot A. One
# Linode-specific wart: their image deploys preserve every partition
# faithfully but zero the MBR's boot code, so shipping this image
# ends by putting its first 440 bytes back (terraform.tf tells that
# story). Loop-mounting a partition needs root, hence sudo;
# everything else here runs unprivileged.
image/disk.img: image/install.cpio $(VMLINUZ) image/OVMF_VARS.fd grub.cfg
	rm -f $@ image/data-scratch.img
	truncate -s $(SYSTEM_DISK_BYTES) $@
	truncate -s $(DATA_SCRATCH_BYTES) image/data-scratch.img
	qemu-system-x86_64 \
		-accel kvm -accel tcg \
		-cpu max \
		-smp 2 \
		-m 2048 \
		-drive if=pflash,format=raw,readonly=on,file=/usr/share/OVMF/OVMF_CODE_4M.fd \
		-drive if=pflash,format=raw,file=image/OVMF_VARS.fd \
		-kernel $(VMLINUZ) -initrd image/install.cpio \
		-append "console=ttyS0 rdinit=/liken liken.machine=node-1 liken.install" \
		-display none \
		-serial file:image/install.log \
		-monitor none \
		-no-reboot \
		-netdev user,id=uplink \
		-device virtio-net-pci,netdev=uplink \
		-netdev user,id=cluster \
		-device virtio-net-pci,netdev=cluster \
		-device virtio-scsi-pci \
		-drive file=$@,format=raw,if=none,id=system \
		-device scsi-hd,drive=system,serial=liken-node-1-system \
		-drive file=image/data-scratch.img,format=raw,if=none,id=data \
		-device scsi-hd,drive=data,serial=liken-node-1-data
	rm -f image/data-scratch.img
	grep -q 'install failed' image/install.log && { tail -5 image/install.log; rm -f $@; exit 1; } || true
	sgdisk --new=8:34:2047 --typecode=8:ef02 --change-name=8:"BIOS boot" $@
	LOOP=$$(sudo losetup --find --show -P $@) && \
		mkdir -p image/slot-a && \
		sudo mount $${LOOP}p1 image/slot-a && \
		sudo grub-install --target=i386-pc \
			--boot-directory=image/slot-a/boot \
			--modules="part_gpt fat" $$LOOP && \
		sudo cp grub.cfg image/slot-a/boot/grub/grub.cfg && \
		sudo umount image/slot-a && \
		sudo losetup -d $$LOOP

clean:
	rm -rf image

.PHONY: all identity kubeconfig clean
