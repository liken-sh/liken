# The dev cluster: the deployment liken is developed against.
#
# Every other domain in this repo produces a piece of the operating
# system; this one is a *user* of it: a complete deployment, from the
# declared side (cluster.yaml and machines/, the manifests the image
# build bakes in) to the virtual hardware that boots them. Physical
# and cloud machines are the real targets; these QEMU guests are where
# everything gets tried first: headless, disposable, and observable
# from the serial console alone. QEMU plays two roles at once here:
# the hardware (CPU, memory, NICs, disks) and the bootloader: -kernel
# and -initrd load the two files that are the entire operating system,
# per the x86 Linux boot protocol. No boot disk, no GRUB, no UEFI.
#
# The lab has a node dimension: the same image boots every machine,
# and NODE picks which machine a given QEMU process is. Each node gets
# its own disks (dist/<node>/), its own MACs, and its own
# liken.machine=<node> kernel parameter, which is how the shared image
# knows which Machine manifest is this machine's. Boot the cluster by
# running each node in its own terminal:
#
#   make run                    the founding leader, node-1
#   make run NODE=node-4        a follower, in a second terminal
#
# The full lab is five machines (node-1 through node-3 the leaders,
# node-4 and node-5 the followers), which is more RAM than most
# laptops want to give QEMU at the default size — MEM sizes each
# guest, and followers are fine at 2048: nothing heavy is pinned to
# them, and the kubelet's own overhead is what's being hosted.
#
# The OS artifacts are other domains' work; this Makefile only points
# at them. The root Makefile is the orchestrator that makes sure they
# exist before booting; run `make run` from the repo root.
NODE ?= node-1
MEM ?= 4096
KERNEL_VERSION := $(strip $(file <../kernel/VERSION))
VMLINUZ := ../kernel/dist/$(KERNEL_VERSION)/vmlinuz
INITRD := ../image/dist/liken.cpio

# Each node's MACs are derived from its number so they never collide:
# QEMU's default would give every guest the same address
# (52:54:00:12:34:56), and two machines sharing a MAC on one segment
# is the kind of failure that presents as anything but what it is.
# 52:54:00 is QEMU's locally-administered prefix; 4c:4b is "LK".
NODE_NUM := $(patsubst node-%,%,$(NODE))
UPLINK_MAC := 52:54:00:4c:4b:0$(NODE_NUM)
CLUSTER_MAC := 52:54:00:4c:4c:0$(NODE_NUM)

# The machine's disks: state, not source, and not part of the two-file
# operating system. They are created blank on purpose: liken's storage
# design starts from a machine and an empty disk, with partitioning and
# formatting done at boot by the machine itself, exactly as they would
# have to happen on hardware that no build system ever touched. The
# names are for the people running the lab; the machine sees two
# anonymous blank disks and learns their purposes only from its
# manifest: a small one for the cluster's state, and a larger one to
# carve up for pod storage. qcow2 is QEMU's copy-on-write format,
# allocated sparsely, so the disks cost the host almost nothing until
# the machine writes to them. Everything lives in the gitignored
# dist/, one directory per node, and `make clean` is the factory
# reset: the next boot finds blank disks and claims them fresh.
dist/$(NODE)/state.qcow2:
	mkdir -p dist/$(NODE)
	qemu-img create -f qcow2 $@ 2G

dist/$(NODE)/pods.qcow2:
	mkdir -p dist/$(NODE)
	qemu-img create -f qcow2 $@ 4G

# The boot disk: where the machine will keep its own operating system
# once it installs itself (milestone 12's story). Blank like the
# others — the machine claims it, formats the two FAT32 system slots,
# and eventually boots from it. Attached third, so the data disks
# keep the /dev/vda and /dev/vdb handles their claims were declared
# with.
dist/$(NODE)/boot.qcow2:
	mkdir -p dist/$(NODE)
	qemu-img create -f qcow2 $@ 2G

# Only the leader node forwards the API port to the host: kubectl on
# the host reaches the cluster through the control plane, and there is
# exactly one (see ../identity/kubeconfig.sh). Forwarding the same
# host port from every node would collide anyway — a useful reminder
# that the forward is to a *cluster*, not to whichever machine.
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
#   -m $(MEM)                Kubernetes-sized memory: the root
#                            filesystem, container images, and every
#                            workload all live in RAM here
#   -append                  the kernel command line: put the kernel's
#                            console on the first serial port, run our
#                            program, by name, as PID 1, and tell it
#                            which machine this is (liken.machine= is
#                            the bootloader's channel for identity;
#                            QEMU is the bootloader here, so identity
#                            rides this Makefile's NODE variable)
#   -display none            there is no screen; liken speaks serial only
#   -serial stdio            ...and that serial port is this terminal
#   -monitor none            don't multiplex QEMU's own control console
#                            onto the same stream; tests read pure liken
#   -no-reboot               a guest-initiated reboot ends QEMU instead
#                            of looping, so boots terminate cleanly.
#                            `run-lab` drops this flag (see below): a
#                            reboot then resets the VM in place
#
# The network is two NICs with two jobs, which is what real fleets
# look like (a management network and a cluster network):
#
#   -netdev user             eth0, the uplink. QEMU's built-in
#                            user-mode network plays DHCP server,
#                            router, NAT, and DNS proxy for the guest
#                            (the 10.0.2.0/24 network), no root or
#                            bridges required on the host. Every guest
#                            gets its own private copy — user-mode NAT
#                            isolates guests from each other, which is
#                            exactly why it can't be the cluster's
#                            wire. On the leader, hostfwd punches one
#                            hole inward: host 127.0.0.1:16443 forwards
#                            to the guest's API server on 6443, which
#                            is what lets kubectl on the host reach the
#                            cluster. A non-default host port, bound to
#                            loopback only, so it can't collide with
#                            any other cluster the host talks to
#   -netdev socket,mcast     eth1, the cluster segment. Every QEMU
#                            process that names the same multicast
#                            group joins the same virtual ethernet
#                            segment: each guest's frames are wrapped
#                            in UDP and multicast to the others, making
#                            a hub with no root, no bridges, and no
#                            daemon. It is a dumb wire on purpose — no
#                            DHCP, no NAT, no DNS — which is precisely
#                            the environment that forces static
#                            addressing, exactly as the Machine
#                            manifests declare. 239.192.0.0/14 is the
#                            organization-local multicast scope (RFC
#                            2365): these frames stay on this host
#   -device virtio-net-pci   the NICs those netdevs attach to; virtio
#                            because our vendored kernel builds that
#                            driver in (CONFIG_VIRTIO_NET=y). Device
#                            order is PCI slot order is probe order,
#                            so the uplink is always eth0 and the
#                            cluster segment always eth1, matching the
#                            names the manifests pin
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
QEMU_EXIT_ON_REBOOT := -no-reboot

# A deliberately wrong hardware clock: `make run RTC=2001-01-01` boots
# the guest believing it's the turn of the millennium, which is the
# milestone-7 drill — watch init's SNTP client discover a
# quarter-century of error, step the clock before k3s starts, and
# write the corrected time back to the RTC. QEMU's default
# (-rtc base=utc) hands the guest the host's clock, so an unset RTC
# means a normal boot.
RTC ?= utc
RTC_BASE := -rtc base=$(RTC)

run: dist/$(NODE)/state.qcow2 dist/$(NODE)/pods.qcow2 dist/$(NODE)/boot.qcow2
	qemu-system-x86_64 \
		-accel kvm -accel tcg \
		-cpu max \
		-m $(MEM) \
		-kernel $(VMLINUZ) \
		-initrd $(INITRD) \
		-append "console=ttyS0 rdinit=/liken liken.machine=$(NODE) $(LIKEN_BOOT_ARGS)" \
		-display none \
		-serial stdio \
		-monitor none \
		$(RTC_BASE) \
		$(QEMU_EXIT_ON_REBOOT) \
		-netdev user,id=uplink$(API_FORWARD) \
		-device virtio-net-pci,netdev=uplink,mac=$(UPLINK_MAC) \
		-netdev socket,id=cluster,mcast=239.192.76.75:26443 \
		-device virtio-net-pci,netdev=cluster,mac=$(CLUSTER_MAC) \
		-drive file=dist/$(NODE)/state.qcow2,format=qcow2,if=none,id=state \
		-device virtio-blk-pci,drive=state,serial=liken-$(NODE)-state \
		-drive file=dist/$(NODE)/pods.qcow2,format=qcow2,if=none,id=pods \
		-device virtio-blk-pci,drive=pods,serial=liken-$(NODE)-pods \
		-drive file=dist/$(NODE)/boot.qcow2,format=qcow2,if=none,id=boot \
		-device virtio-blk-pci,drive=boot,serial=liken-$(NODE)-boot

# One-shot boots for debugging and automation: liken.oneshot tells init
# not to restart k3s: its first exit powers the machine off, QEMU
# exits, and the console log is a complete, bounded record of the boot.
run-once: LIKEN_BOOT_ARGS = liken.oneshot
run-once: run

# A reboot-capable run: identical to `run` but without -no-reboot, so
# a guest-initiated restart resets the VM in place. QEMU reloads
# -kernel and -initrd on reset, the qcow2 disks persist, and the
# serial console shows the shutdown and the next boot as one stream:
# the way to watch a staged spec get applied end to end.
run-lab: QEMU_EXIT_ON_REBOOT :=
run-lab: run

# Grow a node's pods disk under the machine, the lab's stand-in for a
# bigger drive or an expanded cloud volume. Only ever run this with
# the machine off: resizing a live qcow2 corrupts it. The next boot
# notices the disk outgrew its partition table, relocates the backup
# GPT to the new end, and grows the remainder role into the new space.
grow-pods:
	qemu-img resize dist/$(NODE)/pods.qcow2 +4G

.PHONY: run run-once run-lab grow-pods clean

clean:
	rm -rf dist
