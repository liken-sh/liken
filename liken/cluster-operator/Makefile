# Builds the static cluster operator binary and packs it into an OCI
# image tarball with image/oci.sh, the same way the machine operator
# is built.
include ../version.mk
DIST ?= dist

# A failed or killed recipe leaves a partly written target that is newer
# than its inputs, and make would treat it as up to date.
.DELETE_ON_ERROR:

all: $(DIST)/liken-cluster-operator-image.tar

# flux.go embeds seed/gotk-components.yaml, the pinned Flux install
# manifest, so the pod needs no hostPath mount to read it. go:embed
# cannot read outside the package directory, so this rule copies the
# file from the flux component's build output.
FLUX_VERSION := $(strip $(file <../flux/VERSION))

seed/gotk-components.yaml: ../flux/dist/$(FLUX_VERSION)/gotk-components.yaml
	cp $< $@

../flux/dist/$(FLUX_VERSION)/gotk-components.yaml: ../flux/VERSION ../flux/pin.toml ../flux/fetch.sh
	$(MAKE) -C ../flux

$(DIST)/liken-cluster-operator: ../go.mod ../go.sum $(wildcard *.go) \
		$(wildcard ../api/*.go) $(wildcard ../cluster/*.go) \
		$(wildcard ../machine/*.go) $(wildcard ../kubernetes/*.go) $(wildcard ../kubernetes/*/*.go) \
		$(wildcard ../../kubernetes/*/*.go) ../../kubernetes/go.mod \
		$(wildcard ../metrics/*.go) \
		seed/gotk-components.yaml $(LIKEN_VERSION_STAMP)
	CGO_ENABLED=0 go build -trimpath \
		-ldflags "-X github.com/liken-sh/liken/liken/machine.Version=$(LIKEN_VERSION)" \
		-o $(DIST)/liken-cluster-operator .

# The operator fetches release channels over HTTPS, and its image has
# no base layer with root certificates, so oci.sh adds the CA bundle
# that the trust component pins.
CA_BUNDLE := ../../trust/dist/$(strip $(file <../../trust/VERSION))/cacert.pem

$(DIST)/liken-cluster-operator-image.tar: $(DIST)/liken-cluster-operator ../image/oci.sh $(CA_BUNDLE)
	DIST=$(abspath $(DIST)) LIKEN_VERSION=$(LIKEN_VERSION) CA_BUNDLE=$(CA_BUNDLE) \
		IMAGE_DESCRIPTION='liken-cluster-operator, which marks the machines that stopped reporting as Lost, writes the status of the Cluster, and gives each machine its turn to reboot.' \
		IMAGE_LICENSES=MIT \
		../image/oci.sh liken-cluster-operator liken.sh/cluster-operator

.PHONY: all clean

clean:
	rm -rf dist
