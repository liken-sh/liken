# The dev cluster's release channel: the deployment side of a
# release.
#
# A public release of liken (releases/, at the repo root) carries the
# generic OS; a fleet upgrades from a channel that carries that OS
# composed with its own deployment layer, because the digest chain
# rooted in the Cluster's catalog must cover the exact bytes its
# machines boot. This Makefile is the lab's instance of that channel,
# and it lives with the deployment, next to the identity and
# manifests it composes in. Any deployment's channel does what this
# one does: build (or download) a versioned OS, pack the layer,
# compose, wrap the installer, publish.
#
#   make release VERSION=0.2.0    build and publish a version to dist/
#   make serve                    serve dist/ to the lab's guests
#   make corrupt VERSION=0.2.0    damage a published release, to drill
#                                 the trust chain (the machine must
#                                 refuse it)
#
# The layer is rebuilt per release even though its inputs rarely
# change, because it is small and cheap and a stale layer would be a
# miserable thing to debug.

VERSION ?=
FAULT ?=

BUILD = build/$(VERSION)
OSBUILD = ../../releases/build/$(VERSION)
KERNEL_VERSION := $(strip $(file <../../kernel/VERSION))

release:
	@test -n "$(VERSION)" || { echo "usage: make release VERSION=x.y.z"; exit 1; }
	$(MAKE) -C ../../releases build VERSION=$(VERSION) FAULT=$(FAULT)
	mkdir -p $(BUILD)/image
	$(OSBUILD)/cli/liken layer .. ../identity \
		../../kernel/dist/$(KERNEL_VERSION) $(BUILD)/image/deployment.cpio
	cat $(OSBUILD)/generic/liken.cpio $(BUILD)/image/deployment.cpio \
		>$(BUILD)/image/liken.cpio
	LIKEN_VERSION=$(VERSION) DIST=$(abspath $(BUILD)/image) ../../image/install.sh
	$(OSBUILD)/cli/liken publish $(BUILD)/image dist $(VERSION)

serve:
	$(MAKE) -C ../../cli
	../../cli/dist/liken serve dist

corrupt:
	@test -n "$(VERSION)" || { echo "usage: make corrupt VERSION=x.y.z"; exit 1; }
	$(MAKE) -C ../../cli
	../../cli/dist/liken corrupt dist $(VERSION)

clean:
	rm -rf build dist

.PHONY: release serve corrupt clean
