# The releases domain: producing liken versions.
#
# Everything else in this repo builds *the* system; this domain builds
# *a version of* the system, on demand, and lays the results out the
# way a release webserver would publish them:
#
#   releases/dist/<version>/vmlinuz       the kernel
#   releases/dist/<version>/liken.cpio    the system (init, k3s, all of it)
#   releases/dist/<version>/install.cpio  the "USB stick" for fresh machines
#   releases/dist/<version>/release.yaml  the document naming the artifacts
#                                         by sha256 digest and size
#
# `make serve` (serve/main.go) exposes that tree over HTTP exactly as
# a real release server would, so the lab's machines can download and
# upgrade across versions without anything leaving this repo. Both
# trees are gitignored: releases are build products, hundreds of
# megabytes each, reproducible from the sources that are committed.
#
# A release is an ordinary build with a different version stamp: the
# init, operators, and image domains all accept LIKEN_VERSION and DIST
# overrides, so this Makefile re-runs their own rules into
# build/<version>/ and never disturbs the normal dist/ trees. After
# cutting a release, a plain `make run` still boots exactly what it
# always did.

# Which version to produce: `make release VERSION=0.2.0`. There is no
# default, because the command exists to name a release, so the name
# must be given.
VERSION ?=

# FAULT selects a deliberate failure: `make release VERSION=x
# FAULT=panic` (or wedge-k3s) builds a release that fails, for
# drilling the fallback machinery (init/fault.go). Only init carries
# the fault, because the drill is about a broken boot, and init is
# the program that boots.
FAULT ?=

BUILD = build/$(VERSION)
KERNEL_VERSION := $(strip $(file <../kernel/VERSION))

release:
	@test -n "$(VERSION)" || { echo "usage: make release VERSION=x.y.z"; exit 1; }
	$(MAKE) -C ../init LIKEN_VERSION=$(VERSION) LIKEN_FAULT=$(FAULT) DIST=$(abspath $(BUILD)/init)
	$(MAKE) -C ../machine-operator LIKEN_VERSION=$(VERSION) DIST=$(abspath $(BUILD)/machine-operator)
	$(MAKE) -C ../cluster-operator LIKEN_VERSION=$(VERSION) DIST=$(abspath $(BUILD)/cluster-operator)
	$(MAKE) -C ../logs LIKEN_VERSION=$(VERSION) DIST=$(abspath $(BUILD)/logs)
	$(MAKE) -C ../image LIKEN_VERSION=$(VERSION) DIST=$(abspath $(BUILD)/image) \
		INIT_DIST=$(abspath $(BUILD)/init) \
		MACHINE_OPERATOR_DIST=$(abspath $(BUILD)/machine-operator) \
		CLUSTER_OPERATOR_DIST=$(abspath $(BUILD)/cluster-operator) \
		LOGS_DIST=$(abspath $(BUILD)/logs) \
		MANIFESTS=../dev-cluster IDENTITY=../dev-cluster/identity \
		$(abspath $(BUILD)/image)/install.cpio
	./publish.sh $(VERSION)

# The release server: a logged file server over dist/, listening on
# 0.0.0.0:8017. The lab's guests reach it at http://10.0.2.2:8017,
# QEMU's built-in address for "the host" on a user-mode network. That
# is the source URL dev-cluster/cluster.yaml seeds.
serve:
	go run ./serve

# Deliberately damage a published release: flip one byte in the middle
# of its liken.cpio without touching release.yaml, so the document
# describes bytes the server no longer has. This drills the case the
# trust chain exists for: a machine must hold at DigestMismatch with
# nothing staged, and must never boot the damaged release.
corrupt:
	@test -n "$(VERSION)" || { echo "usage: make corrupt VERSION=x.y.z"; exit 1; }
	./corrupt.sh $(VERSION)

clean:
	rm -rf build dist

.PHONY: release serve corrupt clean
