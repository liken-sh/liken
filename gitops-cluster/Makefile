# The GitOps lab: the repo's second deployment, beside the dev
# cluster. The dev cluster stays a purely local fleet, so everyday
# drills never depend on a forge. This lab is the fleet whose
# declared state lives in a git repository, kept for developing
# liken's GitOps feature.
#
# dev-cluster/Makefile teaches the whole QEMU arrangement: the
# disks, the firmware, the two NICs, and every flag on the run
# command. This Makefile repeats none of that teaching. It carries
# the same machinery in its smallest form and notes what this lab
# chooses differently:
#
#   * Its own multicast segment and its own API forward port, so
#     both labs can run at the same time without sharing a wire.
#   * UEFI only. The firmware dialects (FIRMWARE=bios, the smoke
#     drills) are the dev cluster's business. This lab drills
#     documents, not boot chains.
#   * 2 GB per guest instead of 1. The sync-engine controllers this
#     lab exists to run do not fit the 1 GB envelope. Proving that
#     envelope stays the dev cluster's job.
#
# Run everything from the repo root (`make run-gitops`,
# `make install-gitops`), which builds the artifacts first.
NODE ?= node-1
BOOT ?= disk
MEM_disk := 2048
MEM_kernel := 4096
MEM ?= $(MEM_$(BOOT))
SMP ?= 4
CONSOLE ?= stdio
KERNEL_VERSION := $(strip $(file <../kernel/VERSION))
VMLINUZ := ../kernel/dist/$(KERNEL_VERSION)/vmlinuz
INITRD = image/initrd.cpio

# Each lab spells its name into its MAC addresses and its multicast
# group, so a stray frame or a duplicate address always names its
# lab: this one uses "G" (0x47) bytes where the dev cluster uses "L"
# (0x4c), and its mcast group's bytes are 71.79, ASCII "GO".
NODE_NUM := $(patsubst node-%,%,$(NODE))
UPLINK_MAC := 52:54:00:47:4f:0$(NODE_NUM)
CLUSTER_MAC := 52:54:00:47:50:0$(NODE_NUM)

# The guest's blank disks and its private UEFI variable store, the
# same shapes as the dev cluster's.
# The state disk is 4G where the dev cluster's is 2G. clusterState,
# the disk's remainder role, holds containerd's images, and this lab
# deliberately runs more of them at once: the flux controllers, the
# ingress tier, and whatever the fleet repository deploys. The 2G
# size met disk pressure the first time a commit enabled traefik.
guests/$(NODE)/state.qcow2:
	mkdir -p guests/$(NODE)
	qemu-img create -f qcow2 $@ 4G

guests/$(NODE)/pods.qcow2:
	mkdir -p guests/$(NODE)
	qemu-img create -f qcow2 $@ 4G

guests/$(NODE)/boot.qcow2:
	mkdir -p guests/$(NODE)
	qemu-img create -f qcow2 $@ 4G

OVMF_CODE := /usr/share/OVMF/OVMF_CODE_4M.fd

guests/$(NODE)/OVMF_VARS.fd:
	mkdir -p guests/$(NODE)
	cp /usr/share/OVMF/OVMF_VARS_4M.fd $@

# The leader forwards the API to the host on 17443, one above the
# dev cluster's 16443. `make kubeconfig-gitops` at the root rewrites
# the computed kubeconfig's server line to this port.
ifeq ($(NODE),node-1)
API_FORWARD := ,hostfwd=tcp:127.0.0.1:17443-:6443
else
API_FORWARD :=
endif

# BOOT=disk boots the machine's own installed slots through the
# firmware; BOOT=kernel is the direct-kernel arrangement for
# machines that have not installed yet. Same idiom, and same
# recursive-assignment caveats, as the dev cluster.
QEMU_BOOT_kernel = -kernel $(VMLINUZ) -initrd $(INITRD) -append "console=ttyS0 rdinit=/liken liken.machine=$(NODE) $(LIKEN_BOOT_ARGS)"
QEMU_BOOT_disk =
QEMU_BOOT = $(QEMU_BOOT_$(BOOT))
QEMU_EXIT_ON_REBOOT :=

run: guests/$(NODE)/state.qcow2 guests/$(NODE)/pods.qcow2 guests/$(NODE)/boot.qcow2 guests/$(NODE)/OVMF_VARS.fd
	qemu-system-x86_64 \
		-accel kvm -accel tcg \
		-cpu max \
		-smp $(SMP) \
		-m $(MEM) \
		-drive if=pflash,format=raw,readonly=on,file=$(OVMF_CODE) \
		-drive if=pflash,format=raw,file=guests/$(NODE)/OVMF_VARS.fd \
		$(QEMU_BOOT) \
		-display none \
		-serial $(CONSOLE) \
		-monitor none \
		$(QEMU_EXIT_ON_REBOOT) \
		-netdev user,id=uplink$(API_FORWARD) \
		-device virtio-net-pci,netdev=uplink,mac=$(UPLINK_MAC) \
		-netdev socket,id=cluster,mcast=239.192.71.79:26444 \
		-device virtio-net-pci,netdev=cluster,mac=$(CLUSTER_MAC) \
		-drive file=guests/$(NODE)/state.qcow2,format=qcow2,if=none,id=state \
		-device virtio-blk-pci,drive=state,serial=liken-$(NODE)-state \
		-drive file=guests/$(NODE)/pods.qcow2,format=qcow2,if=none,id=pods \
		-device virtio-blk-pci,drive=pods,serial=liken-$(NODE)-pods \
		-drive file=guests/$(NODE)/boot.qcow2,format=qcow2,if=none,id=boot \
		-device virtio-blk-pci,drive=boot,serial=liken-$(NODE)-boot

# A one-shot boot for debugging: k3s's first exit powers the
# machine off, and the console log is a bounded record of the boot.
run-once: BOOT = kernel
run-once: LIKEN_BOOT_ARGS = liken.oneshot
run-once: QEMU_EXIT_ON_REBOOT := -no-reboot
run-once: run

# The install boot: claim the boot disk, copy the release into slot
# A, write the firmware's boot entries, and power off. After this,
# `make run` boots the machine from its own disk.
install: BOOT = kernel
install: INITRD = image/install.cpio
install: LIKEN_BOOT_ARGS = liken.install
install: QEMU_EXIT_ON_REBOOT := -no-reboot
install: run

.PHONY: run run-once install clean

clean:
	rm -rf guests
