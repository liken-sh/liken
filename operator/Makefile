# The operator domain: the in-cluster half of the Machine API.
#
# Two artifacts: the operator binary (a static Go build, same
# discipline as init's) and the OCI image tarball that carries it into
# the cluster, packaged by the recipe the image domain owns
# (image/oci.sh). main.go's header comment explains what an operator
# is and why this one uses no Kubernetes libraries.
#
# The version and output directory are overridable for the same
# reason init's are: the releases domain rebuilds this operator under
# a different version stamp into its own tree, through these same
# rules.
LIKEN_VERSION ?= $(strip $(file <../VERSION))
DIST ?= dist

all: $(DIST)/liken-operator-image.tar

$(DIST)/liken-operator: ../go.mod ../go.sum $(wildcard *.go) $(wildcard ../machine/*.go) ../VERSION
	CGO_ENABLED=0 go build -trimpath \
		-ldflags "-X github.com/chrisguidry/liken/machine.Version=$(LIKEN_VERSION)" \
		-o $(DIST)/liken-operator .

$(DIST)/liken-operator-image.tar: $(DIST)/liken-operator ../image/oci.sh
	DIST=$(abspath $(DIST)) LIKEN_VERSION=$(LIKEN_VERSION) ../image/oci.sh liken-operator liken.sh/operator

.PHONY: all clean

clean:
	rm -rf dist
