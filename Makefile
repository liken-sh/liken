# liken is assembled, not compiled: every artifact is a plain file
# derived from pinned inputs, which makes GNU Make's model — files,
# prerequisites, timestamps — the natural way to drive the build.
#
# The structure follows the repo's rule of organizing by domain: each
# domain directory (kernel/, and soon init/ and image/) has its own
# Makefile that owns its rules and can be run standalone with
# `make -C <domain>`. This root Makefile names the artifacts the domains
# exchange and delegates the work of producing them.

# The kernel's version pin names its artifacts, so the root reads it too:
# downstream rules (the image build, QEMU) will depend on these real
# files, not on phony targets.
KERNEL_VERSION := $(strip $(file <kernel/VERSION))
KERNEL_DIST := kernel/dist/$(KERNEL_VERSION)

all: kernel init

# Because the version is part of the artifact's name, a pin bump changes
# the target path itself and Make rebuilds without any staleness
# cleverness; the prerequisites here just mirror what the kernel domain
# would say about itself.
$(KERNEL_DIST)/vmlinuz: kernel/VERSION kernel/fetch.sh
	$(MAKE) -C kernel

kernel: $(KERNEL_DIST)/vmlinuz

# liken itself: the Go program that boots as PID 1 (the story is in
# init/main.go's header comment).
init/dist/liken: $(wildcard init/*.go) init/go.mod init/go.sum
	$(MAKE) -C init

init: init/dist/liken

# The bootable initramfs: the image domain packs liken (and someday
# modules, k3s, and CA certificates) into the cpio archive the kernel
# unpacks at boot.
image/dist/liken.cpio: init/dist/liken image/Makefile
	$(MAKE) -C image

image: image/dist/liken.cpio

# Boot the whole thing. QEMU acts as the bootloader here: -kernel and
# -initrd load the two files that are the entire operating system, per
# the x86 Linux boot protocol — no disk, no GRUB, no UEFI. Flag by flag:
#
#   -accel kvm -accel tcg    hardware virtualization when available
#                            (near-native speed), pure emulation when
#                            not (CI), in that order of preference
#   -m 512                   more than enough for a kernel and one Go
#                            process; k3s will raise this someday
#   -append                  the kernel command line: put the kernel's
#                            console on the first serial port, then run
#                            our program — by name — as PID 1
#   -display none            there is no screen; liken speaks serial only
#   -serial stdio            ...and that serial port is this terminal
#   -monitor none            don't multiplex QEMU's own control console
#                            onto the same stream; tests read pure liken
#   -no-reboot               a guest-initiated reboot ends QEMU instead
#                            of looping, so boots terminate cleanly
run: $(KERNEL_DIST)/vmlinuz image/dist/liken.cpio
	qemu-system-x86_64 \
		-accel kvm -accel tcg \
		-m 512 \
		-kernel $(KERNEL_DIST)/vmlinuz \
		-initrd image/dist/liken.cpio \
		-append "console=ttyS0 rdinit=/liken" \
		-display none \
		-serial stdio \
		-monitor none \
		-no-reboot

clean:
	$(MAKE) -C kernel clean
	$(MAKE) -C init clean
	$(MAKE) -C image clean

.PHONY: all kernel init image run clean
