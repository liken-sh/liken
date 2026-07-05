# The image domain: where the pieces become a bootable system.
#
# The system is an initramfs: a cpio archive the kernel unpacks into an
# in-memory root filesystem at boot. There is no disk, no bootloader,
# and no installer — the entire OS is two files, the kernel and this
# archive, and QEMU boots them directly. build.sh tells the full story
# of what goes in and why.

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
		../operator/dist/liken-operator-image.tar \
		$(wildcard ../operator/manifests/*.yaml) \
		../VERSION
	./build.sh

.PHONY: all clean

all: dist/liken.cpio

clean:
	rm -rf dist
