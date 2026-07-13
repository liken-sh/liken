# The machine operator domain: the node-local half of operating the
# OS through the Kubernetes API.
#
# Two artifacts: the operator binary (a static Go build, same
# discipline as init's) and the OCI image tarball that carries it into
# the cluster, packaged by the recipe the image domain owns
# (image/oci.sh). main.go's header comment explains what an operator
# is, why this one uses no Kubernetes libraries, and where the line
# sits between it and the cluster operator.
#
# version.mk supplies the version stamp; the releases domain
# overrides it and the output directory to rebuild this operator
# under a release's name into its own tree, through these same rules.
include ../version.mk
DIST ?= dist

all: $(DIST)/liken-machine-operator-image.tar

$(DIST)/liken-machine-operator: ../go.mod ../go.sum $(wildcard *.go) \
		$(wildcard ../machine/*.go) $(wildcard ../kubernetes/*.go) $(LIKEN_VERSION_STAMP)
	CGO_ENABLED=0 go build -trimpath \
		-ldflags "-X github.com/liken-sh/liken/machine.Version=$(LIKEN_VERSION)" \
		-o $(DIST)/liken-machine-operator .

# The operator downloads releases over HTTPS, and its image has no
# base layer to supply root certificates, so the machine's own
# vendored trust bundle rides in the image (oci.sh explains where it
# lands).
CA_BUNDLE := ../trust/dist/$(strip $(file <../trust/VERSION))/cacert.pem

$(DIST)/liken-machine-operator-image.tar: $(DIST)/liken-machine-operator ../image/oci.sh $(CA_BUNDLE)
	DIST=$(abspath $(DIST)) LIKEN_VERSION=$(LIKEN_VERSION) CA_BUNDLE=$(CA_BUNDLE) ../image/oci.sh liken-machine-operator liken.sh/machine-operator

.PHONY: all clean

clean:
	rm -rf dist
