# The cluster operator domain covers the fleet-level half of
# operating the OS through the Kubernetes API.
#
# This domain builds two artifacts, following the same pattern as the
# machine operator: the static binary, and the OCI image tarball that
# image/oci.sh wraps around it. main.go's header comment explains
# what this program watches, what it writes, and why it needs no
# leader election.
include ../version.mk
DIST ?= dist

all: $(DIST)/liken-cluster-operator-image.tar

# The engine seed rides inside the binary (flux.go embeds seed/),
# because this operator deliberately has no hostPath mounts to read
# the image's files through. The flux domain renders the seed at its
# pinned version, and this rule copies it to where go:embed can see
# it; embed paths cannot leave the package directory.
FLUX_VERSION := $(strip $(file <../flux/VERSION))

seed/gotk-components.yaml: ../flux/dist/$(FLUX_VERSION)/gotk-components.yaml
	cp $< $@

../flux/dist/$(FLUX_VERSION)/gotk-components.yaml: ../flux/VERSION ../flux/fetch.sh
	$(MAKE) -C ../flux

$(DIST)/liken-cluster-operator: ../go.mod ../go.sum $(wildcard *.go) \
		$(wildcard ../machine/*.go) $(wildcard ../kubernetes/*.go) \
		seed/gotk-components.yaml $(LIKEN_VERSION_STAMP)
	CGO_ENABLED=0 go build -trimpath \
		-ldflags "-X github.com/liken-sh/liken/machine.Version=$(LIKEN_VERSION)" \
		-o $(DIST)/liken-cluster-operator .

# The operator polls release channels over HTTPS. Its image has no
# base layer to supply root certificates, so the machine's own
# vendored trust bundle rides in the image. oci.sh explains where the
# bundle lands.
CA_BUNDLE := ../trust/dist/$(strip $(file <../trust/VERSION))/cacert.pem

$(DIST)/liken-cluster-operator-image.tar: $(DIST)/liken-cluster-operator ../image/oci.sh $(CA_BUNDLE)
	DIST=$(abspath $(DIST)) LIKEN_VERSION=$(LIKEN_VERSION) CA_BUNDLE=$(CA_BUNDLE) ../image/oci.sh liken-cluster-operator liken.sh/cluster-operator

.PHONY: all clean

clean:
	rm -rf dist
