# The releases domain: producing versions of liken itself, for
# anyone.
#
# Everything else in this repo builds *the* system; this domain builds
# *a version of* the system. A release carries the generic OS and the
# toolkit, with no deployment inside (bundle.go describes the set),
# laid out the way a release server serves it:
#
#   releases/dist/<version>/vmlinuz       the kernel
#   releases/dist/<version>/liken.sqfs    the system image: init, k3s,
#                                         all of it as a read-only
#                                         filesystem a machine mounts
#                                         as its root, and nobody's
#                                         identity or manifests
#   releases/dist/<version>/boot.cpio     the boot archive: the small
#                                         initramfs the boot loader
#                                         stages, init and the early
#                                         boot's modules
#   releases/dist/<version>/liken         the toolkit: mint an
#                                         identity, pack a deployment
#                                         layer, assemble install
#                                         media — no repo or build
#                                         required
#   releases/dist/<version>/release.yaml  the document naming each
#                                         artifact by sha256 digest
#                                         and recording the versions
#                                         of the components inside
#
# Because nothing in the bundle depends on a deployment, these digests
# are stable and publishable, and this channel is the only one there
# is: every fleet upgrades from it directly, each machine carrying its
# own deployment layer between its slots. The `serve` target below is
# the lab's stand-in for the releases on the liken.sh website; the
# dev cluster's guests fetch from it at http://10.0.2.2:8017.
#
# A release is an ordinary build with a different version stamp: the
# init, operators, cli, and image domains all accept LIKEN_VERSION and
# DIST overrides, so `build` re-runs their own rules into
# build/<version>/ and never disturbs the normal dist/ trees. After
# cutting a release, a plain `make run` still boots exactly what it
# always did.

# Which version to produce: `make release VERSION=2026.07.11-001`.
# There is no default, because the command exists to name a release,
# so the name must be given. The grammar (a calendar date and a
# serial, every field zero-padded) is defined in machine/versions.go
# and re-checked here before any building happens, so a typo costs a
# second rather than a full build; versioning.md explains the scheme
# and the release process around it.
VERSION ?=

# FAULT selects a deliberate failure: `make build VERSION=2026.07.11-001
# FAULT=panic` (or wedge-k3s) builds a release that fails, for
# drilling the fallback machinery (init/fault.go). Only init carries
# the fault, because the drill is about a broken boot, and init is
# the program that boots.
FAULT ?=

BUILD = build/$(VERSION)

# The vendored components and the upstream versions of them that this
# release will carry, read from each domain's VERSION file. The
# release document records these because the release's own version is
# a date that deliberately says nothing about contents.
KERNEL_VERSION := $(strip $(file <../kernel/VERSION))
K3S_VERSION := $(strip $(file <../k3s/VERSION))
XTABLES_VERSION := $(strip $(file <../xtables/VERSION))
TRUST_VERSION := $(strip $(file <../trust/VERSION))
E2FSPROGS_VERSION := $(strip $(file <../e2fsprogs/VERSION))
OPENISCSI_VERSION := $(strip $(file <../open-iscsi/VERSION))
NFSUTILS_VERSION := $(strip $(file <../nfs-utils/VERSION))
SYSTEMDBOOT_VERSION := $(strip $(file <../systemd-boot/VERSION))

# The version-stamped build: every liken component rebuilt under
# VERSION into build/<version>/, generic image included. This is the
# release's input, and nothing else's: deployments never build.
build:
	@test -n "$(VERSION)" || { echo "usage: make build VERSION=yyyy.mm.dd-nnn"; exit 1; }
	@echo "$(VERSION)" | grep -Eq '^[0-9]{4}\.[0-9]{2}\.[0-9]{2}-[0-9]{3}$$' \
		|| { echo "VERSION must be yyyy.mm.dd-nnn (for example 2026.07.11-001)"; exit 1; }
	$(MAKE) -C ../init LIKEN_VERSION=$(VERSION) LIKEN_FAULT=$(FAULT) DIST=$(abspath $(BUILD)/init)
	$(MAKE) -C ../machine-operator LIKEN_VERSION=$(VERSION) DIST=$(abspath $(BUILD)/machine-operator)
	$(MAKE) -C ../cluster-operator LIKEN_VERSION=$(VERSION) DIST=$(abspath $(BUILD)/cluster-operator)
	$(MAKE) -C ../logs LIKEN_VERSION=$(VERSION) DIST=$(abspath $(BUILD)/logs)
	$(MAKE) -C ../open-iscsi LIKEN_VERSION=$(VERSION) DIST=$(abspath $(BUILD)/open-iscsi)
	$(MAKE) -C ../cli LIKEN_VERSION=$(VERSION) DIST=$(abspath $(BUILD)/cli)
	$(MAKE) -C ../image LIKEN_VERSION=$(VERSION) DIST=$(abspath $(BUILD)/generic) \
		INIT_DIST=$(abspath $(BUILD)/init) \
		MACHINE_OPERATOR_DIST=$(abspath $(BUILD)/machine-operator) \
		CLUSTER_OPERATOR_DIST=$(abspath $(BUILD)/cluster-operator) \
		LOGS_DIST=$(abspath $(BUILD)/logs) \
		OPENISCSI_DIST=$(abspath $(BUILD)/open-iscsi)

# The release: the build, bundled. The toolkit in the bundle is the
# same binary that lays out the bundle, which is the kind of
# circularity a bootstrapping tool earns. The report ends with the
# catalog entry a deployment commits to its Cluster to adopt this
# release.
release: build
	$(MAKE) -C ../systemd-boot
	$(BUILD)/cli/liken bundle \
		../kernel/dist/$(KERNEL_VERSION)/vmlinuz \
		$(BUILD)/generic/liken.sqfs \
		$(BUILD)/generic/boot.cpio \
		$(BUILD)/cli/liken \
		../systemd-boot/dist/$(SYSTEMDBOOT_VERSION)/systemd-bootx64.efi \
		dist $(VERSION) \
		kernel=$(KERNEL_VERSION) \
		k3s=$(K3S_VERSION) \
		xtables=$(XTABLES_VERSION) \
		trust=$(TRUST_VERSION) \
		e2fsprogs=$(E2FSPROGS_VERSION) \
		open-iscsi=$(OPENISCSI_VERSION) \
		nfs-utils=$(NFSUTILS_VERSION) \
		systemd-boot=$(SYSTEMDBOOT_VERSION)

# Serve the channel over HTTP: the lab's release server, standing in
# for the website. The dev cluster's guests reach it at
# http://10.0.2.2:8017, the source URL their Cluster document
# declares.
serve:
	$(MAKE) -C ../cli
	../cli/dist/liken serve dist

clean:
	rm -rf build dist

.PHONY: build release serve clean
