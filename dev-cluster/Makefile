# The dev cluster: the deployment liken is developed against.
#
# Every other domain in this repo produces a piece of the operating
# system; this one is a user of it: a complete deployment, from the
# declared side (cluster.yaml and machines/, the manifests the image
# build bakes in) to the virtual hardware that boots them. Physical
# and cloud machines are the real targets; these QEMU guests are where
# everything gets tried first: headless, disposable, and observable
# from the serial console alone. QEMU provides the hardware (CPU,
# memory, NICs, disks, and OVMF's real UEFI firmware), and each
# machine boots from its own disk, from the system slots it installed
# itself onto (`make install NODE=x`, once). BOOT=kernel keeps the
# older arrangement, where QEMU also acts as the bootloader and loads
# the two files that are the entire operating system via
# -kernel/-initrd, per the x86 Linux boot protocol. That is the
# fallback for machines that haven't installed yet, and it is what
# run-once and install use.
#
# The lab is parameterized by node: the same image boots every
# machine, and NODE picks which machine a given QEMU process is. Each
# node gets its own disks (guests/<node>/), its own MACs, and its own
# liken.machine=<node> kernel parameter, which is how the shared image
# knows which Machine manifest is this machine's. Boot the cluster by
# running each node in its own terminal:
#
#   make run                    the founding leader, node-1
#   make run NODE=node-4        a follower, in a second terminal
#
# The full lab is five machines (node-1 through node-3 the leaders,
# node-4 and node-5 the followers), which at the default size is more
# RAM than most laptops can spare for QEMU. MEM sizes each guest, and
# followers are fine at 2048: nothing heavy is pinned to them, so
# their memory mostly covers the kubelet's own overhead.
#
# SMP sizes each guest's CPU count, and it matters more than it looks:
# QEMU's default is a single vCPU, and a Kubernetes server cannot live
# on one core. k3s runs the whole control plane in one process, and
# etcd is in there too, with hard real-time-ish needs: the raft leader
# must send heartbeats every 500ms and fsync its log promptly, or the
# other members conclude it has died. On one core those deadlines
# compete with the apiserver, the scheduler, the kubelet, and every
# workload on the machine; any burst of work (a compaction, a garbage
# collection, a log flood) owns the core for seconds, heartbeats go
# out late, etcd logs "leader is overloaded", and API requests queue
# up past the apiserver's 60-second limit. Four vCPUs is modest for a
# server and leaves those deadlines met even when the machine is busy.
#
# The OS artifacts are other domains' work; this Makefile only points
# at them. The root Makefile is the orchestrator that makes sure they
# exist before booting; run `make run` from the repo root.
NODE ?= node-1
MEM ?= 4096
SMP ?= 4
KERNEL_VERSION := $(strip $(file <../kernel/VERSION))
VMLINUZ := ../kernel/dist/$(KERNEL_VERSION)/vmlinuz
INITRD := image/liken.cpio

# Each node's MACs are derived from its number so they never collide:
# QEMU's default would give every guest the same address
# (52:54:00:12:34:56), and two machines sharing a MAC on one segment
# is very hard to diagnose, because the symptoms point everywhere
# except the MAC addresses.
# 52:54:00 is QEMU's locally-administered prefix; 4c:4b is "LK".
NODE_NUM := $(patsubst node-%,%,$(NODE))
UPLINK_MAC := 52:54:00:4c:4b:0$(NODE_NUM)
CLUSTER_MAC := 52:54:00:4c:4c:0$(NODE_NUM)

# The machine's disks. These hold state, not source, and they are not
# part of the two-file operating system. They are deliberately created
# blank: liken's storage design starts from a machine and an empty
# disk, with partitioning and formatting done at boot by the machine
# itself, exactly as they would have to happen on hardware that no
# build system ever touched. The names are for the people running the
# lab; the machine sees two anonymous blank disks and learns their
# purposes only from its manifest: a small one for the cluster's
# state, and a larger one to carve up for pod storage. qcow2 is QEMU's
# copy-on-write format, allocated sparsely, so the disks cost the host
# almost nothing until the machine writes to them. Everything lives in
# the gitignored guests/, one directory per node, and `make clean` acts
# as a factory reset: the next boot finds blank disks and claims them
# fresh.
guests/$(NODE)/state.qcow2:
	mkdir -p guests/$(NODE)
	qemu-img create -f qcow2 $@ 2G

guests/$(NODE)/pods.qcow2:
	mkdir -p guests/$(NODE)
	qemu-img create -f qcow2 $@ 4G

# The boot disk: where the machine will keep its own operating system
# once it installs itself (milestone 12). It starts blank like the
# others; the machine claims it, formats the two FAT32 system slots,
# and eventually boots from it. It is attached third, so the data
# disks keep the /dev/vda and /dev/vdb handles their claims were
# declared with.
guests/$(NODE)/boot.qcow2:
	mkdir -p guests/$(NODE)
	qemu-img create -f qcow2 $@ 2G

# Real UEFI firmware for the guests: OVMF is TianoCore edk2 (the same
# codebase physical motherboards ship) built for virtual machines
# (Ubuntu's ovmf package). It comes as two pflash images, and the
# split mirrors how a physical board is organized. OVMF_CODE is the
# firmware program itself, read-only and shared by every guest.
# OVMF_VARS is the firmware's writable variable store, the equivalent
# of the battery-backed CMOS where boot entries live. Each node gets
# its own private copy, which survives guest reboots the way a real
# board's NVRAM survives power cuts; deleting it with `make clean`
# has the same effect as pulling the CMOS battery.
OVMF_CODE := /usr/share/OVMF/OVMF_CODE_4M.fd

guests/$(NODE)/OVMF_VARS.fd:
	mkdir -p guests/$(NODE)
	cp /usr/share/OVMF/OVMF_VARS_4M.fd $@

# Only the leader node forwards the API port to the host: kubectl on
# the host reaches the cluster through the control plane, and there is
# exactly one (see ../identity/kubeconfig.sh). Forwarding the same
# host port from every node would collide anyway. The forward reaches
# the cluster as a whole, not any particular machine.
ifeq ($(NODE),node-1)
API_FORWARD := ,hostfwd=tcp:127.0.0.1:16443-:6443
else
API_FORWARD :=
endif

# Boot the machine. Flag by flag:
#
#   -accel kvm -accel tcg    hardware virtualization when available
#                            (near-native speed), pure emulation when
#                            not (CI), in that order of preference
#   -cpu max                 the fullest CPU the accelerator can offer,
#                            crucially including RDRAND. QEMU's default
#                            model lacks it, and without any entropy
#                            source the kernel RNG never initializes, so
#                            the first getrandom() in userspace blocks
#                            forever (liken's DHCP client draws one for
#                            its transaction IDs)
#   -smp $(SMP)              enough cores for Kubernetes: etcd's raft
#                            deadlines cannot share a single vCPU with
#                            the rest of the control plane (see SMP
#                            above)
#   -m $(MEM)                enough memory for Kubernetes: the root
#                            filesystem, container images, and every
#                            workload all live in RAM here
#   -drive if=pflash (x2)    the machine's firmware: OVMF's code and
#                            this node's private variable store, in
#                            that order (the firmware expects unit 0
#                            to be code and unit 1 to be NVRAM). With
#                            -kernel also present, OVMF boots the
#                            fw_cfg kernel directly: the "USB stick"
#                            arrangement that stands in for an
#                            installer until machines boot from their
#                            own disks
#   -append                  the kernel command line: put the kernel's
#                            console on the first serial port, run our
#                            program, by name, as PID 1, and tell it
#                            which machine this is (liken.machine= is
#                            the bootloader's channel for identity;
#                            QEMU is the bootloader here, so the
#                            identity comes from this Makefile's NODE
#                            variable)
#   -display none            there is no screen; liken uses only the
#                            serial console
#   -serial stdio            ...and that serial port is this terminal
#   -monitor none            don't multiplex QEMU's own control console
#                            onto the same stream; tests need to read
#                            liken's output and nothing else
#
# The network is two NICs with two jobs, which is what real fleets
# look like (a management network and a cluster network):
#
#   -netdev user             eth0, the uplink. QEMU's built-in
#                            user-mode network provides DHCP, routing,
#                            NAT, and DNS proxying for the guest
#                            (the 10.0.2.0/24 network), no root or
#                            bridges required on the host. Every guest
#                            gets its own private copy: user-mode NAT
#                            isolates guests from each other, which is
#                            exactly why it can't be the cluster's
#                            wire. On the leader, hostfwd opens one
#                            inbound path: host 127.0.0.1:16443
#                            forwards to the guest's API server on
#                            6443, which is what lets kubectl on the
#                            host reach the cluster. The host port is
#                            non-default and bound to loopback only,
#                            so it can't collide with any other
#                            cluster the host talks to
#   -netdev socket,mcast     eth1, the cluster segment. Every QEMU
#                            process that names the same multicast
#                            group joins the same virtual ethernet
#                            segment: each guest's frames are wrapped
#                            in UDP and multicast to the others, making
#                            a hub with no root, no bridges, and no
#                            daemon. The segment is deliberately a
#                            dumb wire, with no DHCP, no NAT, and no
#                            DNS. That is precisely the environment
#                            that forces static addressing, exactly as
#                            the Machine manifests declare.
#                            239.192.0.0/14 is the organization-local
#                            multicast scope (RFC 2365): these frames
#                            stay on this host
#   -device virtio-net-pci   the NICs those netdevs attach to; virtio
#                            because our vendored kernel builds that
#                            driver in (CONFIG_VIRTIO_NET=y). Device
#                            order determines PCI slot order, which
#                            determines probe order, so the uplink is
#                            always eth0 and the cluster segment
#                            always eth1, matching the names the
#                            manifests pin
#   -drive                   the machine's disks (the qcow2s above).
#                            if=none only registers a backing file
#                            under an id; the -device lines below do
#                            the attaching, the same declare-then-
#                            attach split as -netdev/-device for the NICs
#   -device virtio-blk-pci   the disk controllers, virtio again for the
#                            same reason (CONFIG_VIRTIO_BLK=y); the
#                            guest sees /dev/vda and /dev/vdb, in
#                            -device order. serial= gives each disk a
#                            serial number the guest can read from
#                            sysfs, standing in for the serial a real
#                            drive would carry, so identifying a disk
#                            by identity rather than by kernel name
#                            works in the lab too
# A guest-initiated reboot resets the VM in place: QEMU reloads the
# firmware (and -kernel/-initrd when present), the qcow2 disks and
# NVRAM persist, and the serial console shows the shutdown and the
# next boot as one stream. That's the behavior a lab machine needs,
# because reboots are how liken applies staged specs and proves new
# releases. If a reboot ended the machine's QEMU process, the machine
# would power off mid-rollout and leave the fleet. The bounded boots
# (run-once, install) add -no-reboot instead: they end in a deliberate
# power off, and if anything reboots unexpectedly they should exit and
# hand the console log back rather than loop.
QEMU_EXIT_ON_REBOOT :=

# Where the boot comes from. BOOT=disk (the default) leaves booting
# entirely to the firmware: no -kernel, no -append; OVMF consults its
# boot entries and loads the OS from the machine's own system slots,
# exactly as real hardware would. That is why a machine must be
# installed (`make install NODE=x`, once) before its first disk boot.
# BOOT=kernel is the direct-kernel arrangement: QEMU itself loads
# vmlinuz and the initramfs, taking the bootloader's role. That is
# the fallback for machines that haven't installed yet, and it is
# what run-once still uses (its extra kernel parameter has to change
# per run, which a boot entry baked at install time cannot do).
# The variables below use recursive assignment (=) deliberately:
# LIKEN_BOOT_ARGS is set per target (run-once, install) and must be
# picked up at recipe expansion.
BOOT ?= disk

# Selected by name at recipe expansion (never ifeq, which evaluates
# at parse time and would ignore the target-specific BOOT overrides
# install and run-once rely on).
QEMU_BOOT_kernel = -kernel $(VMLINUZ) -initrd $(INITRD) -append "console=ttyS0 rdinit=/liken liken.machine=$(NODE) $(LIKEN_BOOT_ARGS)"
QEMU_BOOT_disk =
QEMU_BOOT = $(QEMU_BOOT_$(BOOT))

# A deliberately wrong hardware clock: `make run RTC=2001-01-01`
# starts the guest with a clock reading the turn of the millennium.
# This is the milestone-7 drill: watch init's SNTP client discover a
# quarter-century of error, step the clock before k3s starts, and
# write the corrected time back to the RTC. QEMU's default
# (-rtc base=utc) gives the guest the host's clock, so an unset RTC
# means a normal boot.
RTC ?= utc
RTC_BASE := -rtc base=$(RTC)

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
		-serial stdio \
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
		-device virtio-blk-pci,drive=boot,serial=liken-$(NODE)-boot

# One-shot boots for debugging and automation: liken.oneshot tells init
# not to restart k3s: its first exit powers the machine off, QEMU
# exits, and the console log is a complete, bounded record of the boot.
run-once: BOOT = kernel
run-once: LIKEN_BOOT_ARGS = liken.oneshot
run-once: QEMU_EXIT_ON_REBOOT := -no-reboot
run-once: run

# The install boot, the lab's equivalent of booting a physical
# machine from a USB installer. It boots the install image
# (liken.cpio carrying the release payload) with liken.install on the
# command line; init claims the boot disk if it's blank, verifies and
# copies the release into slot A, writes the firmware's boot entries,
# and powers off, which ends QEMU. From then on, `make run BOOT=disk`
# boots the machine from its own disk. The install is idempotent: if
# it is interrupted, running it again converges.
install: BOOT = kernel
install: INITRD = image/install.cpio
install: LIKEN_BOOT_ARGS = liken.install
install: QEMU_EXIT_ON_REBOOT := -no-reboot
install: run

# Grow a node's pods disk under the machine, the lab's stand-in for a
# bigger drive or an expanded cloud volume. Only ever run this with
# the machine off: resizing a live qcow2 corrupts it. The next boot
# notices the disk outgrew its partition table, relocates the backup
# GPT to the new end, and grows the remainder role into the new space.
grow-pods:
	qemu-img resize guests/$(NODE)/pods.qcow2 +4G

.PHONY: run run-once install grow-pods clean

clean:
	rm -rf guests
