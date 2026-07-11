# The image domain, where the pieces are assembled into a bootable
# system.
#
# The system is an initramfs: a cpio archive the kernel unpacks into an
# in-memory root filesystem at boot. There is no disk, no bootloader,
# and no installer: the entire OS is two files, the kernel and this
# archive, and QEMU boots them directly. build.sh documents everything
# that goes in and why.
#
# The archive built here is generic: it carries no identity and no
# manifests, so it is the OS's own artifact and lands in this domain's
# dist/ by default. What makes it a deployment's image is the
# deployment layer (layer.go), concatenated on by whoever owns the
# deployment (the root Makefile for the dev cluster, the releases
# domain for a release build). version.mk supplies the version stamp;
# it and the input locations are overridable because the releases
# domain rebuilds this same archive under a different version into
# its own tree.
include ../version.mk
DIST ?= dist
INIT_DIST ?= ../init/dist
MACHINE_OPERATOR_DIST ?= ../machine-operator/dist
CLUSTER_OPERATOR_DIST ?= ../cluster-operator/dist
LOGS_DIST ?= ../logs/dist
OPENISCSI_VERSION := $(strip $(file <../open-iscsi/VERSION))
OPENISCSI_DIST ?= ../open-iscsi/dist/$(OPENISCSI_VERSION)
NFSUTILS_VERSION := $(strip $(file <../nfs-utils/VERSION))

KERNEL_VERSION := $(strip $(file <../kernel/VERSION))
K3S_VERSION := $(strip $(file <../k3s/VERSION))
XTABLES_VERSION := $(strip $(file <../xtables/VERSION))
TRUST_VERSION := $(strip $(file <../trust/VERSION))

# The prerequisites here mirror the ones the root Makefile's rule
# declares (from its side of the directory boundary); when either
# list changes, change the other to match.
$(DIST)/liken.cpio: build.sh $(shell find etc -type f) \
		$(INIT_DIST)/liken \
		../kernel/dist/$(KERNEL_VERSION)/vmlinuz \
		../k3s/dist/$(K3S_VERSION)/k3s \
		../xtables/dist/$(XTABLES_VERSION)/bin/xtables-legacy-multi \
		../trust/dist/$(TRUST_VERSION)/cacert.pem \
		$(OPENISCSI_DIST)/iscsid $(OPENISCSI_DIST)/iscsiadm \
		$(OPENISCSI_DIST)/iscsid-image.tar \
		$(wildcard ../open-iscsi/manifests/*.yaml) \
		../open-iscsi/modules.conf ../open-iscsi/iscsid.conf \
		../nfs-utils/dist/$(NFSUTILS_VERSION)/mount.nfs \
		../nfs-utils/modules.conf \
		$(MACHINE_OPERATOR_DIST)/liken-machine-operator-image.tar \
		$(wildcard ../machine-operator/manifests/*.yaml) \
		$(CLUSTER_OPERATOR_DIST)/liken-cluster-operator-image.tar \
		$(wildcard ../cluster-operator/manifests/*.yaml) \
		$(wildcard ../manifests/*.yaml) \
		$(LOGS_DIST)/liken-logs-image.tar \
		$(wildcard ../logs/manifests/*.yaml) \
		$(LIKEN_VERSION_STAMP)
	LIKEN_VERSION=$(LIKEN_VERSION) DIST=$(abspath $(DIST)) \
		INIT_DIST=$(abspath $(INIT_DIST)) \
		MACHINE_OPERATOR_DIST=$(abspath $(MACHINE_OPERATOR_DIST)) \
		CLUSTER_OPERATOR_DIST=$(abspath $(CLUSTER_OPERATOR_DIST)) \
		LOGS_DIST=$(abspath $(LOGS_DIST)) \
		OPENISCSI_DIST=$(abspath $(OPENISCSI_DIST)) \
		./build.sh

.PHONY: all clean

all: $(DIST)/liken.cpio

# The install image (install.sh) is a deployment artifact — it wraps
# the deployment's composed liken.cpio — so deployments invoke
# install.sh against their own trees; there is no rule for it here.

clean:
	rm -rf dist
