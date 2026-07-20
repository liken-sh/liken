# This is the image domain, where the pieces are assembled into a
# bootable system.
#
# The system is two artifacts (build.sh documents everything that
# goes into each, and why): liken.sqfs, the whole OS as a read-only
# squashfs image that init mounts as the root filesystem, and
# boot.cpio, the small initramfs that the boot loader stages in RAM,
# holding init and the early boot's modules and nothing more.
#
# Both are generic: they carry no identity and no manifests, so they
# are the OS's own artifacts, and they land in this domain's dist/ by
# default. What makes the system belong to a deployment is the
# deployment layer (layer.go), loaded as a further initrd by whoever
# owns the deployment (the root Makefile for the dev cluster, the
# releases domain for a release build). version.mk supplies the
# version stamp. Both the stamp and the input locations can be
# overridden, because the releases domain rebuilds these same
# artifacts under a different version into its own tree.
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
HWDATA_VERSION := $(strip $(file <../hwdata/VERSION))

# The prerequisites here mirror the ones the root Makefile's rule
# declares, from its side of the directory boundary. When either list
# changes, change the other one to match.
$(DIST)/liken.sqfs $(DIST)/boot.cpio &: build.sh boot-modules.conf \
		$(shell find etc -type f) \
		$(INIT_DIST)/liken \
		../kernel/dist/$(KERNEL_VERSION)/vmlinuz \
		../k3s/dist/$(K3S_VERSION)/k3s \
		../xtables/dist/$(XTABLES_VERSION)/bin/xtables-legacy-multi \
		../trust/dist/$(TRUST_VERSION)/cacert.pem \
		../hwdata/dist/$(HWDATA_VERSION)/pci.ids \
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
		$(wildcard ../machine/manifests/*.yaml) \
		$(wildcard ../cluster/manifests/*.yaml) \
		$(wildcard ../kubernetes/manifests/*.yaml) \
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

all: $(DIST)/liken.sqfs $(DIST)/boot.cpio

clean:
	rm -rf dist
