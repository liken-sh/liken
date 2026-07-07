# The image domain: where the pieces become a bootable system.
#
# The system is an initramfs: a cpio archive the kernel unpacks into an
# in-memory root filesystem at boot. There is no disk, no bootloader,
# and no installer: the entire OS is two files, the kernel and this
# archive, and QEMU boots them directly. build.sh tells the full story
# of what goes in and why.
#
# MANIFESTS points the build at the deployment's Cluster and Machine
# manifests (a directory carrying cluster.yaml and machines/*.yaml).
# It has no default on purpose: this domain is the production OS, and
# which machines exist is a deployment's decision, not the OS's. The
# root Makefile passes the repo's own lab (dev-cluster/); without it
# the image carries no manifests, which is still a valid machine.
MANIFESTS ?=

KERNEL_VERSION := $(strip $(file <../kernel/VERSION))
K3S_VERSION := $(strip $(file <../k3s/VERSION))
XTABLES_VERSION := $(strip $(file <../xtables/VERSION))
TRUST_VERSION := $(strip $(file <../trust/VERSION))

dist/liken.cpio: build.sh $(shell find etc -type f) \
		../init/dist/liken \
		../kernel/dist/$(KERNEL_VERSION)/vmlinuz \
		../k3s/dist/$(K3S_VERSION)/k3s \
		../xtables/dist/$(XTABLES_VERSION)/bin/xtables-legacy-multi \
		../trust/dist/$(TRUST_VERSION)/cacert.pem \
		../identity/dist/tls/server-ca.crt \
		../identity/dist/token \
		../operator/dist/liken-operator-image.tar \
		$(wildcard ../operator/manifests/*.yaml) \
		../VERSION
	./build.sh $(MANIFESTS)

.PHONY: all clean

all: dist/liken.cpio

# The install image: liken.cpio plus a concatenated wrapper archive
# carrying the release payload the installer verifies and copies to
# disk. install.sh tells the story.
dist/install.cpio: dist/liken.cpio ../kernel/dist/$(KERNEL_VERSION)/vmlinuz install.sh
	./install.sh

clean:
	rm -rf dist
