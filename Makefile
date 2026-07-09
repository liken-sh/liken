# Every artifact in liken is a plain file with real prerequisites,
# which makes GNU Make's model (files, prerequisites, timestamps)
# the natural way to drive the build. The inputs come two ways. Most
# of the system is vendored: the kernel, k3s, the xtables binaries,
# and the trust store are each fetched at a pinned version and
# verified. The parts that are liken itself, the init, the two
# operators, and the log relays, are the Go programs compiled here.
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

all: kernel k3s xtables trust e2fsprogs init machine-operator cluster-operator logs identity image

# Because the version is part of the artifact's name, a pin bump
# changes the target path itself and Make rebuilds with no extra
# staleness tracking. The prerequisites here mirror the ones the
# kernel domain's own Makefile declares.
$(KERNEL_DIST)/vmlinuz: kernel/VERSION kernel/fetch.sh
	$(MAKE) -C kernel

kernel: $(KERNEL_DIST)/vmlinuz

# k3s packages all of Kubernetes as one pinned, verified download
# (see k3s/fetch.sh).
$(K3S_DIST)/k3s: k3s/VERSION k3s/fetch.sh
	$(MAKE) -C k3s

k3s: $(K3S_DIST)/k3s

# The netfilter userspace tools are vendored from the same project
# that builds k3s's own bundled copy (see xtables/fetch.sh).
$(XTABLES_DIST)/bin/xtables-legacy-multi: xtables/VERSION xtables/fetch.sh
	$(MAKE) -C xtables

xtables: $(XTABLES_DIST)/bin/xtables-legacy-multi

# These are the CA certificates the machine trusts, pinned by
# snapshot date (see trust/fetch.sh).
$(TRUST_DIST)/cacert.pem: trust/VERSION trust/fetch.sh
	$(MAKE) -C trust

trust: $(TRUST_DIST)/cacert.pem

# mke2fs is the program init execs to make a filesystem on a claimed
# disk. It is a static binary, vendored (see e2fsprogs/fetch.sh).
$(E2FSPROGS_DIST)/mke2fs: e2fsprogs/VERSION e2fsprogs/fetch.sh
	$(MAKE) -C e2fsprogs

e2fsprogs: $(E2FSPROGS_DIST)/mke2fs

# This is liken itself, the Go program that boots as PID 1 (see
# init/main.go's header comment). It shares the machine package (the
# Machine API as Go types) with the operator, so both rebuild when
# that package changes.
init/dist/liken: $(wildcard init/*.go) go.mod go.sum \
		$(wildcard machine/*.go) VERSION
	$(MAKE) -C init

init: init/dist/liken

# The machine operator is the node-local half of operating the OS
# through the cluster's API, and the cluster operator is the
# fleet-level half: one pod per machine versus one pod per fleet (each
# main.go's header comment draws the line). Both are packaged as
# hand-assembled OCI images (image/oci.sh, the recipe every
# in-cluster binary shares) and both share the kubernetes package,
# the raw API client, so both rebuild when it changes.
machine-operator/dist/liken-machine-operator-image.tar: $(wildcard machine-operator/*.go) \
		go.mod go.sum image/oci.sh \
		$(wildcard machine/*.go) $(wildcard kubernetes/*.go) VERSION
	$(MAKE) -C machine-operator

machine-operator: machine-operator/dist/liken-machine-operator-image.tar

cluster-operator/dist/liken-cluster-operator-image.tar: $(wildcard cluster-operator/*.go) \
		go.mod go.sum image/oci.sh \
		$(wildcard machine/*.go) $(wildcard kubernetes/*.go) VERSION
	$(MAKE) -C cluster-operator

cluster-operator: cluster-operator/dist/liken-cluster-operator-image.tar

# The log relays carry the machine's host-level log streams (the
# kernel's, init's, k3s's, containerd's) into the cluster as pod
# logs. Packaged exactly like the operator: one static binary in a
# hand-assembled OCI image (see logs/main.go and image/oci.sh).
logs/dist/liken-logs-image.tar: $(wildcard logs/*.go) \
		go.mod go.sum image/oci.sh \
		$(wildcard machine/*.go) VERSION
	$(MAKE) -C logs

logs: logs/dist/liken-logs-image.tar

# The cluster's identity is a set of certificate authorities and the
# join token, minted before any machine boots (see identity/mint.sh).
# The token can exist this early because the CA it hashes already
# exists. An identity belongs to a deployment, not to the OS, so the
# identity domain takes the output directory as an input, and this
# root Makefile points it at the repo's own deployment, the same way
# the image build gets the dev cluster's manifests. The keys are
# gitignored and the artifacts carry no version; losing or remaking
# them just gives the next boot a new identity.
IDENTITY_DIR := dev-cluster/identity

$(IDENTITY_DIR)/tls/server-ca.crt $(IDENTITY_DIR)/token &: identity/mint.sh
	$(MAKE) -C identity DIST=$(abspath $(IDENTITY_DIR))

identity: $(IDENTITY_DIR)/tls/server-ca.crt $(IDENTITY_DIR)/token

# This is an operator's admin credential, computed offline from the
# client CA; the machine never has to provide it (see
# identity/kubeconfig.sh). Use it explicitly, so no kubeconfig you
# already have is ever touched:
#
#   kubectl --kubeconfig dev-cluster/identity/kubeconfig get nodes
kubeconfig: $(IDENTITY_DIR)/tls/server-ca.crt
	$(MAKE) -C identity DIST=$(abspath $(IDENTITY_DIR)) kubeconfig

# This is the bootable initramfs: the image domain packs liken and
# everything k3s needs into the cpio archive the kernel unpacks at
# boot. The image domain is production code and carries no manifests
# or identity of its own, and produces nothing of its own either: the
# assembled archive bakes a deployment's manifests and identity, so
# it is the deployment's artifact and lands in the deployment's
# directory. This root Makefile is where the OS build meets this
# repo's own deployment, so it points the build at the dev cluster's
# manifests and identity and lands the archive beside them.
IMAGE_DIR := dev-cluster/image

$(IMAGE_DIR)/liken.cpio: init/dist/liken $(KERNEL_DIST)/vmlinuz $(K3S_DIST)/k3s \
		$(XTABLES_DIST)/bin/xtables-legacy-multi \
		$(TRUST_DIST)/cacert.pem \
		$(E2FSPROGS_DIST)/mke2fs \
		$(IDENTITY_DIR)/tls/server-ca.crt $(IDENTITY_DIR)/token \
		machine-operator/dist/liken-machine-operator-image.tar \
		$(wildcard machine-operator/manifests/*.yaml) \
		cluster-operator/dist/liken-cluster-operator-image.tar \
		$(wildcard cluster-operator/manifests/*.yaml) \
		$(wildcard manifests/*.yaml) \
		logs/dist/liken-logs-image.tar \
		$(wildcard logs/manifests/*.yaml) \
		dev-cluster/cluster.yaml $(wildcard dev-cluster/machines/*.yaml) \
		image/build.sh $(wildcard image/inventory/*.go) \
		$(shell find image/etc -type f) image/Makefile
	$(MAKE) -C image MANIFESTS=../dev-cluster \
		IDENTITY=$(abspath $(IDENTITY_DIR)) DIST=$(abspath $(IMAGE_DIR))

image: $(IMAGE_DIR)/liken.cpio

# Boot the dev cluster's machines, the QEMU guests that stand in for
# the physical and cloud machines liken really targets (the virtual
# hardware and every QEMU flag are documented in dev-cluster/
# Makefile). NODE picks which machine runs in this terminal. To run
# the whole cluster, start one `make run` for the founding leader,
# then a `make run NODE=node-N` for each remaining machine, each in
# its own terminal. A guest reboot resets the VM in place, so the
# console shows a staged spec or a release upgrade applied end to
# end, with the shutdown and the next boot in one stream. As with
# every target here, the root Makefile only makes sure the artifacts
# exist, in order, before handing off.
run: $(KERNEL_DIST)/vmlinuz $(IMAGE_DIR)/liken.cpio
	$(MAKE) -C dev-cluster run

# One-shot boots are for debugging and automation. The liken.oneshot
# flag tells init not to restart k3s: when k3s first exits, the
# machine powers off, QEMU exits, and the console log is a complete,
# bounded record of the boot.
run-once: $(KERNEL_DIST)/vmlinuz $(IMAGE_DIR)/liken.cpio
	$(MAKE) -C dev-cluster run-once

# The install image is liken.cpio carrying the release payload, which
# the installer verifies and copies onto a machine's own disk.
$(IMAGE_DIR)/install.cpio: $(IMAGE_DIR)/liken.cpio $(KERNEL_DIST)/vmlinuz image/install.sh
	$(MAKE) -C image MANIFESTS=../dev-cluster \
		IDENTITY=$(abspath $(IDENTITY_DIR)) DIST=$(abspath $(IMAGE_DIR)) \
		$(abspath $(IMAGE_DIR))/install.cpio

# Install a machine: boot it once from the "USB stick" (the install
# image via -kernel), let it put this release on its own system
# slots, and power off. After that, `make run NODE=x` boots it from
# that disk.
install: $(IMAGE_DIR)/install.cpio
	$(MAKE) -C dev-cluster install

# Produce a liken release: the same system rebuilt under a different
# version stamp, published to releases/dist/<version>/ the way a
# release webserver lays it out (see the releases/ Makefile for the
# full explanation). The root's contribution is the vendored inputs:
# a release rebuilds init, the operators, and the image, but the
# kernel, k3s, and the other vendored artifacts are pinned by their
# own domains and shared by every release.
release: kernel k3s xtables trust e2fsprogs identity
	$(MAKE) -C releases release

# Serve the published releases to the lab over HTTP; the guests reach
# this at http://10.0.2.2:8017, the source URL the dev cluster's
# Cluster document declares.
serve:
	$(MAKE) -C releases serve

# Cleaning includes the dev cluster's disks. If every domain's
# artifacts were removed but the machine state stayed behind, the
# next boot would still carry the old cluster's state, and that would
# not be a clean boot.
clean:
	$(MAKE) -C releases clean
	$(MAKE) -C dev-cluster clean
	$(MAKE) -C kernel clean
	$(MAKE) -C k3s clean
	$(MAKE) -C xtables clean
	$(MAKE) -C trust clean
	$(MAKE) -C e2fsprogs clean
	$(MAKE) -C init clean
	$(MAKE) -C machine-operator clean
	$(MAKE) -C cluster-operator clean
	$(MAKE) -C logs clean
	$(MAKE) -C identity DIST=$(abspath $(IDENTITY_DIR)) clean
	$(MAKE) -C image IDENTITY=$(abspath $(IDENTITY_DIR)) DIST=$(abspath $(IMAGE_DIR)) clean

.PHONY: all kernel k3s xtables trust e2fsprogs init machine-operator cluster-operator logs identity kubeconfig image run run-once install release serve clean
