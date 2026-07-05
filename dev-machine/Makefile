# The dev machine: the virtual hardware liken is developed on.
#
# Every other domain in this repo produces a piece of the operating
# system; this one provides a machine to run it on. Physical and cloud
# machines are the real targets; this QEMU guest is where everything
# gets tried first: headless, disposable, and observable from the
# serial console alone. QEMU plays two roles at once here: the hardware
# (CPU, memory, NIC, disks) and the bootloader: -kernel and -initrd
# load the two files that are the entire operating system, per the x86
# Linux boot protocol. No boot disk, no GRUB, no UEFI.
#
# The OS artifacts are other domains' work; this Makefile only points
# at them. The root Makefile is the orchestrator that makes sure they
# exist before booting; run `make run` from the repo root.
KERNEL_VERSION := $(strip $(file <../kernel/VERSION))
VMLINUZ := ../kernel/dist/$(KERNEL_VERSION)/vmlinuz
INITRD := ../image/dist/liken.cpio

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
# the machine writes to them. Both live in the gitignored dist/, and
# `make clean` is the factory reset: the next boot finds blank disks
# and claims them fresh.
dist/state.qcow2:
	mkdir -p dist
	qemu-img create -f qcow2 $@ 2G

dist/pods.qcow2:
	mkdir -p dist
	qemu-img create -f qcow2 $@ 4G

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
#   -m 4096                  Kubernetes-sized memory: the root
#                            filesystem, container images, and every
#                            workload all live in RAM here
#   -append                  the kernel command line: put the kernel's
#                            console on the first serial port, then run
#                            our program, by name, as PID 1
#   -display none            there is no screen; liken speaks serial only
#   -serial stdio            ...and that serial port is this terminal
#   -monitor none            don't multiplex QEMU's own control console
#                            onto the same stream; tests read pure liken
#   -no-reboot               a guest-initiated reboot ends QEMU instead
#                            of looping, so boots terminate cleanly.
#                            `run-lab` drops this flag (see below): a
#                            reboot then resets the VM in place
#   -netdev user             QEMU's built-in user-mode network: it plays
#                            DHCP server, router, NAT, and DNS proxy for
#                            the guest (the 10.0.2.0/24 network), no root
#                            or bridges required on the host. hostfwd
#                            punches one hole inward: host 127.0.0.1:16443
#                            forwards to the guest's API server on 6443,
#                            which is what lets kubectl on the host reach
#                            the cluster (see ../identity/kubeconfig.sh).
#                            A non-default host port, bound to loopback
#                            only, so it can't collide with any other
#                            cluster the host talks to
#   -device virtio-net-pci   the NIC we attach it to; virtio because
#                            our vendored kernel builds that driver in
#                            (CONFIG_VIRTIO_NET=y); QEMU's default e1000
#                            is a module we don't ship
#   -drive                   the machine's disks (the qcow2s above).
#                            if=none only registers a backing file
#                            under an id; the -device lines below do
#                            the attaching, the same declare-then-
#                            attach split as -netdev/-device for the NIC
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

run: dist/state.qcow2 dist/pods.qcow2
	qemu-system-x86_64 \
		-accel kvm -accel tcg \
		-cpu max \
		-m 4096 \
		-kernel $(VMLINUZ) \
		-initrd $(INITRD) \
		-append "console=ttyS0 rdinit=/liken $(LIKEN_BOOT_ARGS)" \
		-display none \
		-serial stdio \
		-monitor none \
		$(QEMU_EXIT_ON_REBOOT) \
		-netdev user,id=net0,hostfwd=tcp:127.0.0.1:16443-:6443 \
		-device virtio-net-pci,netdev=net0 \
		-drive file=dist/state.qcow2,format=qcow2,if=none,id=state \
		-device virtio-blk-pci,drive=state,serial=liken-lab-state \
		-drive file=dist/pods.qcow2,format=qcow2,if=none,id=pods \
		-device virtio-blk-pci,drive=pods,serial=liken-lab-pods

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

# Grow the pods disk under the machine, the lab's stand-in for a
# bigger drive or an expanded cloud volume. Only ever run this with
# the machine off: resizing a live qcow2 corrupts it. The next boot
# notices the disk outgrew its partition table, relocates the backup
# GPT to the new end, and grows the remainder role into the new space.
grow-pods:
	qemu-img resize dist/pods.qcow2 +4G

.PHONY: run run-once run-lab grow-pods clean

clean:
	rm -rf dist
