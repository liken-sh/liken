# The open-iscsi domain: the iSCSI initiator userspace, the host half
# of the iscsi feature (plans/completed/17-network-storage-clients.md).
#
# The static binaries, iscsid and iscsiadm, come from the userland
# domain, whose buildroot build compiles them from pinned source with
# the recipe in userland/iscsi/. CSI drivers run the host's iscsiadm,
# so those binaries ship at /sbin in every image. This domain packages
# the same iscsid as an OCI image tarball, which carries it into the
# cluster as the feature's DaemonSet (manifests/iscsid.yaml), with the
# recipe the image domain owns (image/oci.sh). This keeps the daemon
# and the tool that talks to it as one build.
#
# The version stamp and output directory can be overridden for the
# same reason as the operators': a release rebuilds the iscsid image
# under its own version into its own tree. The binaries do not vary by
# liken version, so this Makefile copies them from the userland domain
# into DIST, beside the image that it packages from them.
include ../version.mk
DIST ?= dist
USERLAND_DIST := ../userland/dist

all: $(DIST)/iscsid-image.tar

$(DIST)/iscsid $(DIST)/iscsiadm &: $(USERLAND_DIST)/sbin/iscsid $(USERLAND_DIST)/sbin/iscsiadm
	@mkdir -p $(DIST)
	cp $(USERLAND_DIST)/sbin/iscsid $(USERLAND_DIST)/sbin/iscsiadm $(DIST)/

$(DIST)/iscsid-image.tar: $(DIST)/iscsid ../image/oci.sh $(LIKEN_VERSION_STAMP)
	DIST=$(abspath $(DIST)) LIKEN_VERSION=$(LIKEN_VERSION) ../image/oci.sh iscsid liken.sh/iscsid

.PHONY: all clean

clean:
	rm -rf dist
