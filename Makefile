# liken is assembled, not compiled: every artifact is a plain file
# derived from pinned inputs, which makes GNU Make's model — files,
# prerequisites, timestamps — the natural way to drive the build.
#
# The structure follows the repo's rule of organizing by domain: each
# domain directory (kernel/, k3s/, init/, image/) has its own
# Makefile that owns its rules and can be run standalone with
# `make -C <domain>`. This root Makefile names the artifacts the domains
# exchange and delegates the work of producing them.

# Version pins name the vendored artifacts, so the root reads them too:
# downstream rules (the image build, QEMU) depend on these real files,
# not on phony targets.
KERNEL_VERSION := $(strip $(file <kernel/VERSION))
KERNEL_DIST := kernel/dist/$(KERNEL_VERSION)
K3S_VERSION := $(strip $(file <k3s/VERSION))
K3S_DIST := k3s/dist/$(K3S_VERSION)

all: kernel k3s init operator identity image

# Because the version is part of the artifact's name, a pin bump changes
# the target path itself and Make rebuilds without any staleness
# cleverness; the prerequisites here just mirror what the kernel domain
# would say about itself.
$(KERNEL_DIST)/vmlinuz: kernel/VERSION kernel/fetch.sh
	$(MAKE) -C kernel

kernel: $(KERNEL_DIST)/vmlinuz

# All of Kubernetes, as one pinned, verified download (the story is in
# k3s/fetch.sh).
$(K3S_DIST)/k3s: k3s/VERSION k3s/fetch.sh
	$(MAKE) -C k3s

k3s: $(K3S_DIST)/k3s

# liken itself: the Go program that boots as PID 1 (the story is in
# init/main.go's header comment). It shares the machine package — the
# Machine API as Go types — with the operator, so both rebuild when it
# changes.
init/dist/liken: $(wildcard init/*.go) go.mod go.sum \
		$(wildcard machine/*.go) VERSION
	$(MAKE) -C init

init: init/dist/liken

# The liken operator: the in-cluster half of the Machine API, packaged
# as a hand-assembled OCI image (the stories are in operator/main.go
# and operator/image.sh).
operator/dist/liken-operator-image.tar: $(wildcard operator/*.go) \
		go.mod go.sum operator/image.sh \
		$(wildcard machine/*.go) VERSION
	$(MAKE) -C operator

operator: operator/dist/liken-operator-image.tar

# The cluster's identity: certificate authorities minted here, in the
# repo, before any machine boots (the story is in identity/mint.sh).
# The keys are gitignored and the artifacts carry no version — losing
# or remaking them just gives the next boot a new identity.
identity/dist/tls/server-ca.crt: identity/mint.sh
	$(MAKE) -C identity

identity: identity/dist/tls/server-ca.crt

# An operator's admin credential, computed offline from the client CA
# — the machine is never asked for it (the story is in
# identity/kubeconfig.sh). Use it explicitly, so no kubeconfig you
# already have is ever touched:
#
#   kubectl --kubeconfig identity/dist/kubeconfig get nodes
kubeconfig: identity/dist/tls/server-ca.crt
	$(MAKE) -C identity kubeconfig

# The bootable initramfs: the image domain packs liken and everything
# k3s needs into the cpio archive the kernel unpacks at boot.
image/dist/liken.cpio: init/dist/liken $(KERNEL_DIST)/vmlinuz $(K3S_DIST)/k3s \
		identity/dist/tls/server-ca.crt \
		operator/dist/liken-operator-image.tar \
		$(wildcard operator/manifests/*.yaml) \
		image/build.sh $(shell find image/etc -type f) image/Makefile
	$(MAKE) -C image

image: image/dist/liken.cpio

# Boot the whole thing. QEMU acts as the bootloader here: -kernel and
# -initrd load the two files that are the entire operating system, per
# the x86 Linux boot protocol — no disk, no GRUB, no UEFI. Flag by flag:
#
#   -accel kvm -accel tcg    hardware virtualization when available
#                            (near-native speed), pure emulation when
#                            not (CI), in that order of preference
#   -cpu max                 the fullest CPU the accelerator can offer —
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
#                            our program — by name — as PID 1
#   -display none            there is no screen; liken speaks serial only
#   -serial stdio            ...and that serial port is this terminal
#   -monitor none            don't multiplex QEMU's own control console
#                            onto the same stream; tests read pure liken
#   -no-reboot               a guest-initiated reboot ends QEMU instead
#                            of looping, so boots terminate cleanly
#   -netdev user             QEMU's built-in user-mode network: it plays
#                            DHCP server, router, NAT, and DNS proxy for
#                            the guest (the 10.0.2.0/24 world), no root
#                            or bridges required on the host. hostfwd
#                            punches one hole inward: host 127.0.0.1:16443
#                            forwards to the guest's API server on 6443,
#                            which is what lets kubectl on the host reach
#                            the cluster (see identity/kubeconfig.sh). A
#                            non-default host port, bound to loopback
#                            only, so it can't collide with any other
#                            cluster the host talks to
#   -device virtio-net-pci   the NIC we attach it to — virtio because
#                            our vendored kernel builds that driver in
#                            (CONFIG_VIRTIO_NET=y); QEMU's default e1000
#                            is a module we don't ship
run: $(KERNEL_DIST)/vmlinuz image/dist/liken.cpio
	qemu-system-x86_64 \
		-accel kvm -accel tcg \
		-cpu max \
		-m 4096 \
		-kernel $(KERNEL_DIST)/vmlinuz \
		-initrd image/dist/liken.cpio \
		-append "console=ttyS0 rdinit=/liken $(LIKEN_BOOT_ARGS)" \
		-display none \
		-serial stdio \
		-monitor none \
		-no-reboot \
		-netdev user,id=net0,hostfwd=tcp:127.0.0.1:16443-:6443 \
		-device virtio-net-pci,netdev=net0

# One-shot boots for debugging and automation: liken.oneshot tells init
# not to resurrect k3s — its first death powers the machine off, QEMU
# exits, and the console log is a complete, bounded record of the boot.
run-once: LIKEN_BOOT_ARGS = liken.oneshot
run-once: run

clean:
	$(MAKE) -C kernel clean
	$(MAKE) -C k3s clean
	$(MAKE) -C init clean
	$(MAKE) -C operator clean
	$(MAKE) -C identity clean
	$(MAKE) -C image clean

.PHONY: all kernel k3s init operator identity kubeconfig image run run-once clean
