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

# The version baked into the image, where the archive lands, and where
# the liken binary and the operator image come from. All of these are
# overridable because the releases domain assembles release-stamped
# images through these same rules, from its own copies of the inputs,
# into its own tree.
LIKEN_VERSION ?= $(strip $(file <../VERSION))
DIST ?= dist
INIT_DIST ?= ../init/dist
OPERATOR_DIST ?= ../operator/dist

KERNEL_VERSION := $(strip $(file <../kernel/VERSION))
K3S_VERSION := $(strip $(file <../k3s/VERSION))
XTABLES_VERSION := $(strip $(file <../xtables/VERSION))
TRUST_VERSION := $(strip $(file <../trust/VERSION))

$(DIST)/liken.cpio: build.sh $(shell find etc -type f) \
		$(INIT_DIST)/liken \
		../kernel/dist/$(KERNEL_VERSION)/vmlinuz \
		../k3s/dist/$(K3S_VERSION)/k3s \
		../xtables/dist/$(XTABLES_VERSION)/bin/xtables-legacy-multi \
		../trust/dist/$(TRUST_VERSION)/cacert.pem \
		../identity/dist/tls/server-ca.crt \
		../identity/dist/token \
		$(OPERATOR_DIST)/liken-operator-image.tar \
		$(wildcard ../operator/manifests/*.yaml) \
		../VERSION
	LIKEN_VERSION=$(LIKEN_VERSION) DIST=$(abspath $(DIST)) \
		INIT_DIST=$(abspath $(INIT_DIST)) OPERATOR_DIST=$(abspath $(OPERATOR_DIST)) \
		./build.sh $(MANIFESTS)

.PHONY: all clean

all: $(DIST)/liken.cpio

# The install image: liken.cpio plus a concatenated wrapper archive
# carrying the release payload the installer verifies and copies to
# disk. install.sh explains how it fits together.
$(DIST)/install.cpio: $(DIST)/liken.cpio ../kernel/dist/$(KERNEL_VERSION)/vmlinuz install.sh
	LIKEN_VERSION=$(LIKEN_VERSION) DIST=$(abspath $(DIST)) ./install.sh

clean:
	rm -rf dist
