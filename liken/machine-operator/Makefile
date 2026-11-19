# Builds the machine operator, a static Go binary, and the OCI image
# tarball that the OS ships it in. The header comment of main.go
# explains what the operator does.
#
# version.mk sets LIKEN_VERSION. releases/Makefile sets LIKEN_VERSION
# and DIST to build the operator for a release into its own directory.
include ../version.mk
DIST ?= dist

# A recipe that fails or is killed can leave a partly written target
# that is newer than its inputs. make deletes it, so the next build
# makes it again.
.DELETE_ON_ERROR:

all: $(DIST)/liken-machine-operator-image.tar

$(DIST)/liken-machine-operator: ../go.mod ../go.sum $(wildcard *.go) \
		$(wildcard ../api/*.go) $(wildcard ../cluster/*.go) \
		$(wildcard ../machine/*.go) $(wildcard ../kubernetes/*.go) $(wildcard ../kubernetes/*/*.go) \
		$(wildcard ../../kubernetes/*/*.go) ../../kubernetes/go.mod \
		$(wildcard ../hardware/*.go) $(wildcard ../metrics/*.go) \
		$(LIKEN_VERSION_STAMP)
	CGO_ENABLED=0 go build -trimpath \
		-ldflags "-X github.com/liken-sh/liken/liken/machine.Version=$(LIKEN_VERSION)" \
		-o $(DIST)/liken-machine-operator .

# The operator downloads releases over HTTPS, and its image has no base
# layer with root certificates, so the image includes the CA bundle
# that the `trust` component pins.
CA_BUNDLE := ../../trust/dist/$(strip $(file <../../trust/VERSION))/cacert.pem

# The PCI ID database gives readable names to the devices in the
# ResourceSlice (dra.go). oci.sh explains why the image has its own
# copy.
PCI_IDS := ../hwdata/dist/$(strip $(file <../hwdata/VERSION))/pci.ids

$(DIST)/liken-machine-operator-image.tar: $(DIST)/liken-machine-operator ../image/oci.sh $(CA_BUNDLE) $(PCI_IDS)
	DIST=$(abspath $(DIST)) LIKEN_VERSION=$(LIKEN_VERSION) CA_BUNDLE=$(CA_BUNDLE) PCI_IDS=$(PCI_IDS) \
		IMAGE_DESCRIPTION='liken-machine-operator, which applies the Machine resource of each machine to the machine and reports its status.' \
		IMAGE_LICENSES=MIT \
		../image/oci.sh liken-machine-operator liken.sh/machine-operator

.PHONY: all clean

clean:
	rm -rf dist
