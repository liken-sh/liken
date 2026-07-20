# The open-iscsi domain: the iSCSI initiator userspace, the host half
# of the iscsi feature (plans/17-network-storage-clients.md).
#
# Two artifacts. The static binaries (iscsid and iscsiadm) come from
# fetch.sh, which builds them from pinned source inside a pinned
# container. CSI drivers run the host's iscsiadm, so those binaries
# ship at /sbin in every image. The OCI image tarball carries iscsid
# into the cluster as the feature's DaemonSet (manifests/iscsid.yaml),
# packaged by the recipe the image domain owns (image/oci.sh), from
# the same binary. This keeps the daemon and the tool that talks to it
# as one build.
#
# The version stamp and output directory can be overridden for the
# same reason as the operators': a release rebuilds the iscsid image
# under its own version into its own tree. The binaries do not vary by
# liken version, so they always come from this domain's
# dist/<version>/, and this Makefile copies them into an overridden
# DIST for packaging.
include ../version.mk
OPENISCSI_VERSION := $(strip $(file <VERSION))
DIST ?= dist/$(OPENISCSI_VERSION)

all: $(DIST)/iscsid-image.tar

dist/$(OPENISCSI_VERSION)/iscsid dist/$(OPENISCSI_VERSION)/iscsiadm &: VERSION fetch.sh
	./fetch.sh

$(DIST)/iscsid-image.tar: dist/$(OPENISCSI_VERSION)/iscsid ../image/oci.sh $(LIKEN_VERSION_STAMP)
	@mkdir -p $(DIST)
	@if [ "$(abspath $(DIST))" != "$(abspath dist/$(OPENISCSI_VERSION))" ]; then \
		cp dist/$(OPENISCSI_VERSION)/iscsid dist/$(OPENISCSI_VERSION)/iscsiadm $(DIST)/; \
	fi
	DIST=$(abspath $(DIST)) LIKEN_VERSION=$(LIKEN_VERSION) ../image/oci.sh iscsid liken.sh/iscsid

.PHONY: all clean

clean:
	rm -rf dist
