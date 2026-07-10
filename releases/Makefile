# The releases domain: producing versions of liken itself, for
# anyone.
#
# Everything else in this repo builds *the* system; this domain builds
# *a version of* the system. A public release carries the generic OS
# and the toolkit, with no deployment inside (bundle.go describes the
# set), laid out the way a release server serves it:
#
#   releases/dist/<version>/vmlinuz       the kernel
#   releases/dist/<version>/liken.cpio    the generic system: init,
#                                         k3s, all of it, and nobody's
#                                         identity or manifests
#   releases/dist/<version>/liken         the toolkit: mint an
#                                         identity, pack a deployment
#                                         layer, publish a channel —
#                                         no repo or build required
#   releases/dist/<version>/release.yaml  the document naming each
#                                         artifact by sha256 digest
#
# Because nothing in the bundle depends on a deployment, these digests
# are stable and publishable. A deployment's own release channel is a
# different thing: the same OS composed with that deployment's layer,
# verified by that deployment's fleet. The repo's own lab keeps its
# channel in dev-cluster/releases/, built from this domain's `build`
# target, which is the same two-layer shape any deployment follows.
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

# The version-stamped build: every liken component rebuilt under
# VERSION into build/<version>/, generic image included. This is the
# input to both kinds of release: `release` below bundles it publicly,
# and dev-cluster/releases composes it with the lab's deployment
# layer.
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

# The public release: the build, bundled. The toolkit in the bundle
# is the same binary that lays out the bundle, which is the kind of
# circularity a bootstrapping tool earns.
release: build
	$(BUILD)/cli/liken bundle \
		../kernel/dist/$(KERNEL_VERSION)/vmlinuz \
		$(BUILD)/generic/liken.cpio \
		$(BUILD)/cli/liken \
		dist $(VERSION)

clean:
	rm -rf build dist

.PHONY: build release clean
