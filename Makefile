# Every artifact in liken is a plain file with real prerequisites,
# which makes GNU Make's model (files, prerequisites, timestamps)
# the natural way to drive the build. The inputs come two ways: most
# of the system is vendored (the kernel, k3s, the xtables binaries,
# the trust store, each a pinned version, fetched and verified), and
# the parts that are liken itself (init and the operator, the two Go
# programs) are compiled here.
#
# The structure follows the repo's rule of organizing by domain: each
# domain directory (kernel/, k3s/, init/, image/, ...) has its own
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
XTABLES_VERSION := $(strip $(file <xtables/VERSION))
XTABLES_DIST := xtables/dist/$(XTABLES_VERSION)
TRUST_VERSION := $(strip $(file <trust/VERSION))
TRUST_DIST := trust/dist/$(TRUST_VERSION)
E2FSPROGS_VERSION := $(strip $(file <e2fsprogs/VERSION))
E2FSPROGS_DIST := e2fsprogs/dist/$(E2FSPROGS_VERSION)

all: kernel k3s xtables trust e2fsprogs init operator identity image

# Because the version is part of the artifact's name, a pin bump
# changes the target path itself and Make rebuilds with no extra
# staleness tracking; the prerequisites here just mirror what the
# kernel domain would say about itself.
$(KERNEL_DIST)/vmlinuz: kernel/VERSION kernel/fetch.sh
	$(MAKE) -C kernel

kernel: $(KERNEL_DIST)/vmlinuz

# All of Kubernetes, as one pinned, verified download (see
# k3s/fetch.sh).
$(K3S_DIST)/k3s: k3s/VERSION k3s/fetch.sh
	$(MAKE) -C k3s

k3s: $(K3S_DIST)/k3s

# The netfilter userspace, vendored from the same project that builds
# k3s's own bundled copy (see xtables/fetch.sh).
$(XTABLES_DIST)/bin/xtables-legacy-multi: xtables/VERSION xtables/fetch.sh
	$(MAKE) -C xtables

xtables: $(XTABLES_DIST)/bin/xtables-legacy-multi

# The CA certificates the machine trusts, pinned by snapshot date (see
# trust/fetch.sh).
$(TRUST_DIST)/cacert.pem: trust/VERSION trust/fetch.sh
	$(MAKE) -C trust

trust: $(TRUST_DIST)/cacert.pem

# mke2fs, the program init execs to make a filesystem on a claimed
# disk; static, vendored (see e2fsprogs/fetch.sh).
$(E2FSPROGS_DIST)/mke2fs: e2fsprogs/VERSION e2fsprogs/fetch.sh
	$(MAKE) -C e2fsprogs

e2fsprogs: $(E2FSPROGS_DIST)/mke2fs

# liken itself: the Go program that boots as PID 1 (see init/main.go's
# header comment). It shares the machine package (the Machine API as
# Go types) with the operator, so both rebuild when it changes.
init/dist/liken: $(wildcard init/*.go) go.mod go.sum \
		$(wildcard machine/*.go) VERSION
	$(MAKE) -C init

init: init/dist/liken

# The liken operator: the in-cluster half of the Machine API, packaged
# as a hand-assembled OCI image (see operator/main.go and
# operator/image.sh).
operator/dist/liken-operator-image.tar: $(wildcard operator/*.go) \
		go.mod go.sum operator/image.sh \
		$(wildcard machine/*.go) VERSION
	$(MAKE) -C operator

operator: operator/dist/liken-operator-image.tar

# The cluster's identity: certificate authorities minted here, in the
# repo, before any machine boots (see identity/mint.sh). The keys are
# gitignored and the artifacts carry no version; losing or remaking
# them just gives the next boot a new identity.
identity/dist/tls/server-ca.crt: identity/mint.sh
	$(MAKE) -C identity

identity: identity/dist/tls/server-ca.crt

# An operator's admin credential, computed offline from the client
# CA; the machine is never asked for it (see identity/kubeconfig.sh).
# Use it explicitly, so no kubeconfig you already have is ever
# touched:
#
#   kubectl --kubeconfig identity/dist/kubeconfig get nodes
kubeconfig: identity/dist/tls/server-ca.crt
	$(MAKE) -C identity kubeconfig

# The bootable initramfs: the image domain packs liken and everything
# k3s needs into the cpio archive the kernel unpacks at boot.
image/dist/liken.cpio: init/dist/liken $(KERNEL_DIST)/vmlinuz $(K3S_DIST)/k3s \
		$(XTABLES_DIST)/bin/xtables-legacy-multi \
		$(TRUST_DIST)/cacert.pem \
		$(E2FSPROGS_DIST)/mke2fs \
		identity/dist/tls/server-ca.crt \
		operator/dist/liken-operator-image.tar \
		$(wildcard operator/manifests/*.yaml) \
		image/build.sh $(shell find image/etc -type f) image/Makefile
	$(MAKE) -C image

image: image/dist/liken.cpio

# Boot the whole thing on the dev machine, the QEMU guest that stands
# in for the physical and cloud machines liken really targets (the
# virtual hardware and every QEMU flag are documented in
# dev-machine/Makefile). The root's job here, as everywhere, is making
# sure the artifacts exist, in order, before handing off.
run: $(KERNEL_DIST)/vmlinuz image/dist/liken.cpio
	$(MAKE) -C dev-machine run

# One-shot boots for debugging and automation: liken.oneshot tells init
# not to restart k3s: its first exit powers the machine off, QEMU
# exits, and the console log is a complete, bounded record of the boot.
run-once: $(KERNEL_DIST)/vmlinuz image/dist/liken.cpio
	$(MAKE) -C dev-machine run-once

# Cleaning includes the dev machine's disks: with every domain's
# artifacts gone, stale machine state would be the only survivor, and
# a "clean" boot that remembers its last cluster isn't clean.
clean:
	$(MAKE) -C dev-machine clean
	$(MAKE) -C kernel clean
	$(MAKE) -C k3s clean
	$(MAKE) -C xtables clean
	$(MAKE) -C trust clean
	$(MAKE) -C e2fsprogs clean
	$(MAKE) -C init clean
	$(MAKE) -C operator clean
	$(MAKE) -C identity clean
	$(MAKE) -C image clean

.PHONY: all kernel k3s xtables trust e2fsprogs init operator identity kubeconfig image run run-once clean
