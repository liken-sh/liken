# The machine operator domain: the node-local half of operating the
# OS through the Kubernetes API.
#
# This domain builds two artifacts: the operator binary (a static Go
# build, with the same discipline as init's) and the OCI image
# tarball that carries it into the cluster, packaged by the recipe
# the image domain owns (image/oci.sh). main.go's header comment
# explains what an operator is, why this one uses no Kubernetes
# libraries, and where the line sits between it and the cluster
# operator.
#
# version.mk supplies the version stamp. The releases domain
# overrides the stamp and the output directory, to rebuild this
# operator under a release's name into its own tree, through these
# same rules.
include ../version.mk
DIST ?= dist

all: $(DIST)/liken-machine-operator-image.tar

$(DIST)/liken-machine-operator: ../go.mod ../go.sum $(wildcard *.go) \
		$(wildcard ../machine/*.go) $(wildcard ../kubernetes/*.go) \
		$(wildcard ../hardware/*.go) $(LIKEN_VERSION_STAMP)
	CGO_ENABLED=0 go build -trimpath \
		-ldflags "-X github.com/liken-sh/liken/machine.Version=$(LIKEN_VERSION)" \
		-o $(DIST)/liken-machine-operator .

# The operator downloads releases over HTTPS, and its image has no
# base layer to supply root certificates. So the machine's own
# vendored trust bundle travels in the image (oci.sh explains where
# it lands).
CA_BUNDLE := ../trust/dist/$(strip $(file <../trust/VERSION))/cacert.pem

# The PCI naming database, for the device inventory's
# human-readable names (dra.go). oci.sh explains why it travels in
# this image, rather than as a mount of the OS's own copy.
PCI_IDS := ../hwdata/dist/$(strip $(file <../hwdata/VERSION))/pci.ids

$(DIST)/liken-machine-operator-image.tar: $(DIST)/liken-machine-operator ../image/oci.sh $(CA_BUNDLE) $(PCI_IDS)
	DIST=$(abspath $(DIST)) LIKEN_VERSION=$(LIKEN_VERSION) CA_BUNDLE=$(CA_BUNDLE) PCI_IDS=$(PCI_IDS) ../image/oci.sh liken-machine-operator liken.sh/machine-operator

.PHONY: all clean

clean:
	rm -rf dist
