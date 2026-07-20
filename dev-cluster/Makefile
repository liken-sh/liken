# The dev cluster: the deployment liken is developed against.
#
# Every other domain in this repo builds one piece of the operating
# system. This domain uses those pieces together, as a complete
# deployment. The deployment has a declared side (cluster.yaml and
# machines/, the manifests that the image build includes) and a
# virtual-hardware side (the QEMU guests that boot from those
# manifests). Physical machines and cloud machines are the real
# targets. The QEMU guests are where the team tries new changes
# first: the guests have no screen, the team can delete and recreate
# them at will, and the team can observe them only through the serial
# console. QEMU provides the hardware: CPU, memory, NICs, disks, and
# firmware (OVMF's real UEFI by default, or SeaBIOS under
# FIRMWARE=bios). Each machine boots from its own disk, from the
# system slots it installed onto itself (run `make install NODE=x`
# once). BOOT=kernel selects a different arrangement: QEMU acts as
# the bootloader itself. It loads the kernel and one composed initrd
# through -kernel and -initrd, following the x86 Linux boot protocol.
# This arrangement serves machines that have not installed yet.
# run-once and install both use it.
#
# The lab takes a node name as a parameter. The same image boots
# every machine; NODE picks which machine a given QEMU process is.
# Each node gets its own disks (guests/<node>/), its own MAC
# addresses, and its own liken.machine=<node> kernel parameter. This
# parameter tells the shared image which Machine manifest belongs to
# this machine. To boot the cluster, run each node in its own
# terminal:
#
#   make run                    the founding leader, node-1
#   make run NODE=node-4        a follower, in a second terminal
#
# MEM sets the memory size of each guest. The default is the smallest
# cloud machine that liken targets: 1 GB, the amount a Linode nanode
# has. The OS runs from a read-only system image on disk, so the
# guest's memory goes to Kubernetes, not to a copy of the OS. Every
# drill that runs at this size proves that this stays true. If a
# machine stops converging at 1 GB, that is a regression in liken. It
# is not a reason to raise this default.
#
# SMP sets each guest's CPU count. This setting matters more than it
# looks: QEMU's default is a single vCPU, and a Kubernetes server
# cannot run on one core. k3s runs the whole control plane in one
# process, and etcd runs inside that process too, with near-real-time
# needs. The raft leader must send heartbeats every 500ms and write
# its log to disk promptly. If it does not, the other members decide
# that it has failed. On one core, those deadlines compete with the
# apiserver, the scheduler, the kubelet, and every workload on the
# machine. Any burst of work (a compaction, a garbage collection, a
# flood of log writes) can occupy the core for seconds. When this
# happens, heartbeats go out late, etcd logs "leader is overloaded",
# and API requests wait in a queue past the apiserver's 60-second
# limit. Four vCPUs is a modest count for a server, and it keeps
# these deadlines even when the machine is busy.
#
# The OS artifacts come from other domains' work; this Makefile only
# points at them. The root Makefile is the orchestrator that makes
# sure they exist before boot. Run `make run` from the repo root.
NODE ?= node-1
MEM ?= 1024
SMP ?= 4
CONSOLE ?= stdio
KERNEL_VERSION := $(strip $(file <../kernel/VERSION))
VMLINUZ := ../kernel/dist/$(KERNEL_VERSION)/vmlinuz
INITRD := image/initrd.cpio

# Each node's MAC addresses come from its node number, so they never
# collide. QEMU's default gives every guest the same address
# (52:54:00:12:34:56). Two machines that share a MAC address on one
# segment are very hard to diagnose, because the symptoms point
# everywhere except the MAC addresses.
# 52:54:00 is QEMU's locally administered prefix. 4c:4b stands for
# "LK".
NODE_NUM := $(patsubst node-%,%,$(NODE))
UPLINK_MAC := 52:54:00:4c:4b:0$(NODE_NUM)
CLUSTER_MAC := 52:54:00:4c:4c:0$(NODE_NUM)

# The machine's disks. These disks hold state, not source code, and
# they are not part of the two-file operating system. The disks start
# blank on purpose. liken's storage design starts from a machine and
# an empty disk: the machine itself partitions and formats the disk
# at boot, the same way it would have to on hardware that no build
# system ever touched. The file names below are for the people who
# run the lab; the machine itself sees two disks with no names, and
# learns their purpose only from its manifest. One disk is small and
# holds the cluster's state. The other disk is larger, for the
# machine to divide up for pod storage. qcow2 is QEMU's copy-on-write
# format. QEMU allocates a qcow2 file sparsely, so a disk costs the
# host almost nothing until the machine writes to it. All disks live
# under the gitignored guests/ directory, one directory for each
# node. `make clean` deletes this directory and acts as a factory
# reset: the next boot finds blank disks and claims them again.
guests/$(NODE)/state.qcow2:
	mkdir -p guests/$(NODE)
	qemu-img create -f qcow2 $@ 2G

guests/$(NODE)/pods.qcow2:
	mkdir -p guests/$(NODE)
	qemu-img create -f qcow2 $@ 4G

# The boot disk: where the machine keeps its own operating system
# once it installs itself (milestone 12). It starts blank like the
# other disks. The machine claims it, formats the two FAT32 system
# slots on it, and later boots from it. QEMU attaches this disk
# third, so the data disks keep the /dev/vda and /dev/vdb device
# names that their claims declared.
guests/$(NODE)/boot.qcow2:
	mkdir -p guests/$(NODE)
	qemu-img create -f qcow2 $@ 2G

# The guest's firmware. liken supports two firmware types, and both
# exist on real machines: UEFI is the firmware on every modern
# motherboard, and BIOS is the firmware on every Linode guest. The lab
# drills both types.
#
# FIRMWARE=uefi (the default) gives the guest real UEFI. OVMF is
# TianoCore edk2, the same codebase that physical motherboards ship,
# built for virtual machines (Ubuntu's ovmf package). OVMF comes as
# two pflash images, and this split matches how a physical board is
# organized. OVMF_CODE is the firmware program itself. It is
# read-only and shared by every guest. OVMF_VARS is the firmware's
# writable variable store, the equivalent of the battery-backed CMOS
# where boot entries live. Each node gets its own private copy of
# OVMF_VARS. This copy survives guest reboots the way a real board's
# NVRAM survives power cuts. Deleting it with `make clean` has the
# same effect as removing the CMOS battery.
#
# FIRMWARE=bios asks QEMU for nothing extra, and this is how QEMU
# gives BIOS: SeaBIOS is its built-in default firmware, and QEMU uses
# it whenever no pflash firmware replaces it. A BIOS machine has no
# variable store, so there is no per-node file to manage either.
# Everything the boot needs lives on the disk itself: the MBR's boot
# code, GRUB's core image, and the environment block that GRUB reads
# in place of variables.
OVMF_CODE := /usr/share/OVMF/OVMF_CODE_4M.fd

guests/$(NODE)/OVMF_VARS.fd:
	mkdir -p guests/$(NODE)
	cp /usr/share/OVMF/OVMF_VARS_4M.fd $@

# Selected by name at recipe expansion, the same idiom as QEMU_BOOT
# below. The prerequisite list is separate because make expands
# prerequisites at parse time: only a UEFI guest needs its private
# variable store created first.
FIRMWARE ?= uefi
QEMU_FIRMWARE_uefi = -drive if=pflash,format=raw,readonly=on,file=$(OVMF_CODE) -drive if=pflash,format=raw,file=guests/$(NODE)/OVMF_VARS.fd
QEMU_FIRMWARE_bios =
QEMU_FIRMWARE = $(QEMU_FIRMWARE_$(FIRMWARE))
FIRMWARE_DEPS_uefi := guests/$(NODE)/OVMF_VARS.fd
FIRMWARE_DEPS_bios :=
FIRMWARE_DEPS := $(FIRMWARE_DEPS_$(FIRMWARE))

# Which disk the firmware boots. QEMU attaches the boot disk third,
# so the data disks keep the /dev/vda and /dev/vdb device names that
# their claims declared. But BIOS firmware tries disks in device
# order, and the data disks come first in that order. Their claimed
# GPTs carry the protective MBR's 0x55AA signature, with no boot code
# behind it. Without bootindex, SeaBIOS would pick one of these
# disks, jump into a block of zeroes, and stop responding.
# bootindex names the boot disk as the one to try. It stands in for a
# cloud console's boot-device setting. The value is 1, not 0, because
# a -kernel boot claims index 0 for the kernel itself; the disk holds
# the next position either way, and that position is first when there
# is no -kernel. Under UEFI, bootindex is absent on purpose: the
# machine's own NVRAM entries choose the boot path there, and liken's
# upgrade mechanics depend on those entries staying authoritative.
QEMU_BOOTINDEX_uefi =
QEMU_BOOTINDEX_bios = ,bootindex=1
QEMU_BOOTINDEX = $(QEMU_BOOTINDEX_$(FIRMWARE))

# Only the leader node forwards the API port to the host. kubectl on
# the host reaches the cluster through the control plane, and the
# cluster has exactly one control plane (see
# ../identity/kubeconfig.go). Forwarding the same host port from
# every node would also collide. This forward reaches the cluster as
# a whole, not any particular machine.
ifeq ($(NODE),node-1)
API_FORWARD := ,hostfwd=tcp:127.0.0.1:16443-:6443
else
API_FORWARD :=
endif

# Boot the machine. Flag by flag:
#
#   -accel kvm -accel tcg    hardware virtualization when available
#                            (near-native speed), or full emulation
#                            when not available (CI), tried in that
#                            order
#   -cpu max                 the fullest CPU that the accelerator can
#                            offer, including RDRAND. QEMU's default
#                            CPU model lacks RDRAND. Without any
#                            entropy source, the kernel RNG never
#                            initializes, so the first getrandom()
#                            call in userspace waits forever (liken's
#                            DHCP client calls it to generate its
#                            transaction IDs)
#   -smp $(SMP)              enough cores for Kubernetes. etcd's raft
#                            deadlines cannot share a single vCPU with
#                            the rest of the control plane (see SMP
#                            above)
#   -m $(MEM)                enough memory for Kubernetes. The OS
#                            itself runs from its system image on
#                            disk, so this memory is the workloads'
#                            budget, not liken's
#   $(QEMU_FIRMWARE)         the machine's firmware. Under UEFI, two
#                            pflash drives: OVMF's code, then this
#                            node's private variable store, in that
#                            order (the firmware expects unit 0 to
#                            hold code and unit 1 to hold NVRAM).
#                            Under BIOS, nothing: SeaBIOS is what
#                            QEMU runs when no pflash file replaces
#                            it. Both firmware types boot the fw_cfg
#                            kernel directly when -kernel is also
#                            present. This is the "USB stick"
#                            arrangement, and it takes the place of an
#                            installer until machines can boot from
#                            their own disks
#   -append                  the kernel command line. It puts the
#                            kernel's console on the first serial
#                            port, runs liken's own program (by name)
#                            as PID 1, and tells that program which
#                            machine this is. liken.machine= is the
#                            bootloader's channel for identity; QEMU
#                            is the bootloader here, so the identity
#                            comes from this Makefile's NODE variable
#   -display none            there is no screen. liken uses only the
#                            serial console
#   -serial $(CONSOLE)       where that serial port goes. The default,
#                            stdio, is this terminal, so a person can
#                            watch the machine boot. Automation has no
#                            terminal to give, so the smoke drill
#                            passes CONSOLE=file:... and reads the
#                            boot record from that file afterward
#   -monitor none            keep QEMU's own control console off this
#                            stream. Tests must read only liken's
#                            output, and nothing else
#
# The network is two NICs, and each NIC has one job. This matches a
# real fleet's shape: a management network and a cluster network.
#
#   -netdev user             eth0, the uplink. QEMU's built-in
#                            user-mode network gives the guest DHCP,
#                            routing, NAT, and DNS proxying (on the
#                            10.0.2.0/24 network), and needs no root
#                            access or bridges on the host. Every
#                            guest gets its own private copy of this
#                            network. Because user-mode NAT isolates
#                            guests from each other, it cannot serve
#                            as the cluster's wire. On the leader,
#                            hostfwd opens one inbound path: host
#                            127.0.0.1:16443 forwards to the guest's
#                            API server on 6443, and this is what lets
#                            kubectl on the host reach the cluster.
#                            The host port is not the default port,
#                            and it is bound to loopback only, so it
#                            cannot collide with any other cluster the
#                            host talks to
#   -netdev socket,mcast     eth1, the cluster segment. Every QEMU
#                            process that names the same multicast
#                            group joins the same virtual ethernet
#                            segment: each guest wraps its frames in
#                            UDP and sends them by multicast to the
#                            others, which makes a hub with no root
#                            access, no bridges, and no daemon needed.
#                            This segment is a plain wire on purpose,
#                            with no DHCP, no NAT, and no DNS. This is
#                            exactly the setup that requires static
#                            addressing, the way the Machine manifests
#                            declare it. 239.192.0.0/14 is the
#                            organization-local multicast scope (RFC
#                            2365), so these frames stay on this host
#   -device virtio-net-pci   the NICs that these netdevs attach to.
#                            They use virtio because our vendored
#                            kernel builds that driver in
#                            (CONFIG_VIRTIO_NET=y). Device order sets
#                            PCI slot order, which sets probe order,
#                            so the uplink is always eth0 and the
#                            cluster segment is always eth1, matching
#                            the names the manifests pin
#   -drive                   the machine's disks (the qcow2 files
#                            above). if=none only registers a backing
#                            file under an id; the -device lines below
#                            do the attaching. This is the same
#                            declare-then-attach split that
#                            -netdev/-device use for the NICs
#   -device virtio-blk-pci   the disk controllers, using virtio for
#                            the same reason (CONFIG_VIRTIO_BLK=y).
#                            The guest sees /dev/vda and /dev/vdb, in
#                            -device order. serial= gives each disk a
#                            serial number that the guest can read
#                            from sysfs. This takes the place of the
#                            serial number a real drive would carry,
#                            so identifying a disk by its identity,
#                            rather than by its kernel name, also
#                            works in the lab
# A reboot that the guest starts itself resets the VM in place: QEMU
# reloads the firmware (and -kernel/-initrd, when present), the
# qcow2 disks and the NVRAM persist, and the serial console shows the
# shutdown and the next boot as one continuous stream. A lab machine
# needs this behavior, because liken uses reboots to apply staged
# specs and to prove new releases. If a reboot ended the machine's
# QEMU process, the machine would power off in the middle of a
# rollout and leave the fleet. The bounded boots (run-once, install)
# add -no-reboot instead. These boots end in a deliberate power off.
# If anything reboots unexpectedly during one of these boots, QEMU
# should exit and return the console log, rather than boot again.
QEMU_EXIT_ON_REBOOT :=

# Where the boot comes from. BOOT=disk (the default) leaves booting
# entirely to the firmware: no -kernel, no -append. OVMF consults its
# boot entries (or, under FIRMWARE=bios, SeaBIOS runs the disk's boot
# code), and the OS loads from the machine's own system slots, the
# same way real hardware would. This is why a machine must be
# installed (`make install NODE=x`, once) before its first disk
# boot.
# BOOT=kernel is the direct-kernel arrangement: QEMU itself loads
# vmlinuz and the initramfs, and takes the bootloader's role. This
# arrangement serves machines that have not installed yet. run-once
# uses it because its extra kernel parameter changes on each run, and
# a boot entry that install wrote in advance cannot do that.
# The variables below use recursive assignment (=) on purpose:
# run-once and install each set LIKEN_BOOT_ARGS for their own target,
# and make must read that value again when it expands the recipe.
BOOT ?= disk

# Selected by name at recipe expansion (never ifeq, which evaluates
# at parse time and would ignore the target-specific BOOT overrides
# install and run-once rely on).
QEMU_BOOT_kernel = -kernel $(VMLINUZ) -initrd $(INITRD) -append "console=ttyS0 rdinit=/liken liken.machine=$(NODE) $(LIKEN_BOOT_ARGS)"
QEMU_BOOT_disk =
QEMU_BOOT = $(QEMU_BOOT_$(BOOT))

# A hardware clock set wrong on purpose: `make run RTC=2001-01-01`
# starts the guest with a clock that reads the turn of the
# millennium. This is the milestone-7 drill: it lets you watch init's
# SNTP client find a 25-year error, step the clock before k3s starts,
# and write the corrected time back to the RTC. QEMU's default (-rtc
# base=utc) gives the guest the host's own clock, so an unset RTC
# produces a normal boot.
RTC ?= utc
RTC_BASE := -rtc base=$(RTC)

# QEMU_EXTRA is the hook for hardware that only some boots need:
# install-stick attaches the USB stick through it. QEMU_EXTRA stays
# empty for every ordinary boot.
QEMU_EXTRA ?=

run: guests/$(NODE)/state.qcow2 guests/$(NODE)/pods.qcow2 guests/$(NODE)/boot.qcow2 $(FIRMWARE_DEPS)
	qemu-system-x86_64 \
		-accel kvm -accel tcg \
		-cpu max \
		-smp $(SMP) \
		-m $(MEM) \
		$(QEMU_FIRMWARE) \
		$(QEMU_BOOT) \
		$(QEMU_EXTRA) \
		-display none \
		-serial $(CONSOLE) \
		-monitor none \
		$(RTC_BASE) \
		$(QEMU_EXIT_ON_REBOOT) \
		-netdev user,id=uplink$(API_FORWARD) \
		-device virtio-net-pci,netdev=uplink,mac=$(UPLINK_MAC) \
		-netdev socket,id=cluster,mcast=239.192.76.75:26443 \
		-device virtio-net-pci,netdev=cluster,mac=$(CLUSTER_MAC) \
		-drive file=guests/$(NODE)/state.qcow2,format=qcow2,if=none,id=state \
		-device virtio-blk-pci,drive=state,serial=liken-$(NODE)-state \
		-drive file=guests/$(NODE)/pods.qcow2,format=qcow2,if=none,id=pods \
		-device virtio-blk-pci,drive=pods,serial=liken-$(NODE)-pods \
		-drive file=guests/$(NODE)/boot.qcow2,format=qcow2,if=none,id=boot \
		-device virtio-blk-pci,drive=boot,serial=liken-$(NODE)-boot$(QEMU_BOOTINDEX)

# One-shot boots, for debugging and automation. liken.oneshot tells
# init not to restart k3s. When k3s exits for the first time, the
# machine powers off, QEMU exits, and the console log becomes a
# complete, bounded record of the boot.
run-once: BOOT = kernel
run-once: LIKEN_BOOT_ARGS = liken.oneshot
run-once: QEMU_EXIT_ON_REBOOT := -no-reboot
run-once: run

# The install boot, the lab's equivalent of booting a physical
# machine from a USB installer. It boots the install image
# (boot.cpio, the layer, and the release payload) with liken.install
# on the command line. init claims the boot disk if the disk is
# blank, verifies and copies the release into slot A, writes the
# firmware's boot entries, and powers off, which ends QEMU. After
# this, `make run BOOT=disk` boots the machine from its own disk. The
# install is idempotent: if something interrupts it, running it again
# finishes the job.
install: BOOT = kernel
install: INITRD = image/install.cpio
install: LIKEN_BOOT_ARGS = liken.install
install: QEMU_EXIT_ON_REBOOT := -no-reboot
install: run

# The install boot in the same way that real hardware takes it: no
# -kernel at all. This target refreshes the varstore to a blank copy
# first, because that is what a new machine's firmware looks like: no
# boot entries. A firmware with no entries falls through to the
# removable-media path, \EFI\BOOT\BOOTX64.EFI on the stick, which is
# systemd-boot's menu. Pick the machine at the console. The installer
# writes this machine's slot entries into this same varstore, and
# `make run` boots from them afterward. Refreshing an installed
# node's varstore is harmless for the same reason: the install
# recreates its entries. The stick uses USB mass storage because that
# matches the hardware shape. The installer never reads the stick
# after boot, because everything it needs already came in the
# initramfs.
install-stick: guests/$(NODE)/state.qcow2 guests/$(NODE)/pods.qcow2 guests/$(NODE)/boot.qcow2
	cp /usr/share/OVMF/OVMF_VARS_4M.fd guests/$(NODE)/OVMF_VARS.fd
	$(MAKE) run NODE=$(NODE) QEMU_EXIT_ON_REBOOT=-no-reboot QEMU_EXTRA="-device qemu-xhci \
		-drive file=image/stick.img,format=raw,if=none,id=stick \
		-device usb-storage,drive=stick"

# The lab's storage server: a stock-Debian guest on the same cluster
# segment. It serves the iSCSI target and the NFSv4 export that the
# fleet's storage-client features are drilled against. It is a test
# fixture, not a liken machine, so it lives in its own small domain
# (storage/), with its own disks and its own clean target. Run it in
# its own terminal, the same way you run any node.
storage:
	$(MAKE) -C storage run

# Grow a node's pods disk under the machine. This stands in for a
# bigger drive or an expanded cloud volume. Run this command only
# while the machine is off: resizing a live qcow2 file corrupts it.
# On the next boot, the machine notices that the disk has grown
# beyond its partition table, moves the backup GPT to the new end of
# the disk, and grows the remainder role into the new space.
grow-pods:
	qemu-img resize guests/$(NODE)/pods.qcow2 +4G

# The unattended boot: it takes node-1 from blank disks to a Ready
# node, and reads the verdict from the cluster's API instead of
# having a person read the console. This is what CI runs.
# smoke-uefi.sh explains the whole arrangement. Run it from the repo
# root (`make smoke-uefi`), which first builds the artifacts and
# creates the kubeconfig that the drill needs.
smoke-uefi:
	./smoke-uefi.sh

# The same drill under BIOS, starting from further back: it installs
# first, then boots the installed disk under SeaBIOS. The UEFI smoke
# drill boots through -kernel, so this drill is the one that exercises
# the install path and the disk boot path from end to end.
# smoke-bios.sh explains the arrangement.
smoke-bios:
	./smoke-bios.sh

.PHONY: run run-once install install-stick storage grow-pods smoke-uefi smoke-bios clean

clean:
	rm -rf guests
