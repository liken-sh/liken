# The releases domain: producing versions of liken itself, for
# anyone.
#
# Everything else in this repo builds *the* system; this domain builds
# *a version of* the system. A release carries the generic OS and the
# toolkit, with no deployment inside (bundle.go describes the set),
# laid out the way a release server serves it:
#
#   releases/dist/<version>/vmlinuz       the kernel
#   releases/dist/<version>/liken.cpio    the generic system: init,
#                                         k3s, all of it, and nobody's
#                                         identity or manifests
#   releases/dist/<version>/liken         the toolkit: mint an
#                                         identity, pack a deployment
#                                         layer, assemble install
#                                         media — no repo or build
#                                         required
#   releases/dist/<version>/release.yaml  the document naming each
#                                         artifact by sha256 digest
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

# Which version to produce: `make release VERSION=0.2.0`. There is no
# default, because the command exists to name a release, so the name
# must be given.
VERSION ?=

# FAULT selects a deliberate failure: `make build VERSION=x
# FAULT=panic` (or wedge-k3s) builds a release that fails, for
# drilling the fallback machinery (init/fault.go). Only init carries
# the fault, because the drill is about a broken boot, and init is
# the program that boots.
FAULT ?=

BUILD = build/$(VERSION)
KERNEL_VERSION := $(strip $(file <../kernel/VERSION))
SYSTEMDBOOT_VERSION := $(strip $(file <../systemd-boot/VERSION))

# The version-stamped build: every liken component rebuilt under
# VERSION into build/<version>/, generic image included. This is the
# release's input, and nothing else's: deployments never build.
build:
	@test -n "$(VERSION)" || { echo "usage: make build VERSION=x.y.z"; exit 1; }
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
		$(BUILD)/generic/liken.cpio \
		$(BUILD)/cli/liken \
		../systemd-boot/dist/$(SYSTEMDBOOT_VERSION)/systemd-bootx64.efi \
		dist $(VERSION)

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
