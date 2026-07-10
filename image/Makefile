# The image domain, where the pieces are assembled into a bootable
# system.
#
# The system is an initramfs: a cpio archive the kernel unpacks into an
# in-memory root filesystem at boot. There is no disk, no bootloader,
# and no installer: the entire OS is two files, the kernel and this
# archive, and QEMU boots them directly. build.sh documents everything
# that goes in and why.
#
# MANIFESTS points the build at the deployment's Cluster and Machine
# manifests (a directory carrying cluster.yaml and machines/*.yaml).
# It deliberately has no default: this domain is the production OS, and
# which machines exist is a deployment's decision, not the OS's. The
# root Makefile passes the repo's own lab (dev-cluster/); without it
# the image carries no manifests, which is still a valid machine.
MANIFESTS ?=

# IDENTITY points the build at the deployment's identity directory:
# the CA tree and join token the identity domain minted (or adopted).
# No default, for the same reason as MANIFESTS. Unlike the manifests,
# the identity is required, because an image with no CAs and no token
# could neither form a cluster nor join one.
IDENTITY ?=

ifeq ($(strip $(IDENTITY)),)
$(error IDENTITY must name the deployment's identity directory, e.g. IDENTITY=../dev-cluster/identity)
endif

# The version baked into the image, where the archive lands, and where
# the liken binary and the operator images come from. DIST has no
# default for the same reason MANIFESTS and IDENTITY don't: the
# assembled image bakes a deployment's manifests and identity, so the
# archive is the deployment's artifact, and it lands in the
# deployment's directory (the root Makefile passes dev-cluster/image;
# the releases domain passes its own build tree).
LIKEN_VERSION ?= $(strip $(file <../VERSION))
DIST ?=
INIT_DIST ?= ../init/dist
MACHINE_OPERATOR_DIST ?= ../machine-operator/dist
CLUSTER_OPERATOR_DIST ?= ../cluster-operator/dist
LOGS_DIST ?= ../logs/dist
OPENISCSI_VERSION := $(strip $(file <../open-iscsi/VERSION))
OPENISCSI_DIST ?= ../open-iscsi/dist/$(OPENISCSI_VERSION)
NFSUTILS_VERSION := $(strip $(file <../nfs-utils/VERSION))

ifeq ($(strip $(DIST)),)
$(error DIST must name the deployment's image directory, e.g. DIST=../dev-cluster/image)
endif

KERNEL_VERSION := $(strip $(file <../kernel/VERSION))
K3S_VERSION := $(strip $(file <../k3s/VERSION))
XTABLES_VERSION := $(strip $(file <../xtables/VERSION))
TRUST_VERSION := $(strip $(file <../trust/VERSION))

# The deployment's manifests are prerequisites, not just an argument:
# the build bakes them in and reads spec.modules out of them, so an
# edited manifest must rebuild the archive. With no MANIFESTS the
# wildcards expand to nothing. The prerequisites here mirror the ones
# the root Makefile's liken.cpio rule declares (from its side of the
# directory boundary); when either list changes, change the other to
# match.
$(DIST)/liken.cpio: build.sh $(shell find etc -type f) \
		$(wildcard inventory/*.go) $(wildcard ../machine/*.go) \
		$(wildcard $(MANIFESTS)/cluster.yaml) \
		$(wildcard $(MANIFESTS)/machines/*.yaml) \
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
		$(IDENTITY)/tls/server-ca.crt \
		$(IDENTITY)/token \
		$(MACHINE_OPERATOR_DIST)/liken-machine-operator-image.tar \
		$(wildcard ../machine-operator/manifests/*.yaml) \
		$(CLUSTER_OPERATOR_DIST)/liken-cluster-operator-image.tar \
		$(wildcard ../cluster-operator/manifests/*.yaml) \
		$(wildcard ../manifests/*.yaml) \
		$(LOGS_DIST)/liken-logs-image.tar \
		$(wildcard ../logs/manifests/*.yaml) \
		../VERSION
	LIKEN_VERSION=$(LIKEN_VERSION) DIST=$(abspath $(DIST)) \
		INIT_DIST=$(abspath $(INIT_DIST)) \
		MACHINE_OPERATOR_DIST=$(abspath $(MACHINE_OPERATOR_DIST)) \
		CLUSTER_OPERATOR_DIST=$(abspath $(CLUSTER_OPERATOR_DIST)) \
		LOGS_DIST=$(abspath $(LOGS_DIST)) \
		OPENISCSI_DIST=$(abspath $(OPENISCSI_DIST)) \
		IDENTITY=$(abspath $(IDENTITY)) \
		./build.sh $(MANIFESTS)

.PHONY: all clean

all: $(DIST)/liken.cpio

# The install image: liken.cpio plus a concatenated wrapper archive
# carrying the release payload the installer verifies and copies to
# disk. install.sh explains how it fits together.
$(DIST)/install.cpio: $(DIST)/liken.cpio ../kernel/dist/$(KERNEL_VERSION)/vmlinuz install.sh
	LIKEN_VERSION=$(LIKEN_VERSION) DIST=$(abspath $(DIST)) ./install.sh

clean:
	rm -rf $(DIST)
