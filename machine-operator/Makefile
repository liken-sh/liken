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
# The version and output directory are overridable for the same
# reason init's are: the releases domain rebuilds this operator under
# a different version stamp into its own tree, through these same
# rules.
LIKEN_VERSION ?= $(strip $(file <../VERSION))
DIST ?= dist

all: $(DIST)/liken-machine-operator-image.tar

$(DIST)/liken-machine-operator: ../go.mod ../go.sum $(wildcard *.go) \
		$(wildcard ../machine/*.go) $(wildcard ../kubernetes/*.go) ../VERSION
	CGO_ENABLED=0 go build -trimpath \
		-ldflags "-X github.com/liken-sh/liken/machine.Version=$(LIKEN_VERSION)" \
		-o $(DIST)/liken-machine-operator .

$(DIST)/liken-machine-operator-image.tar: $(DIST)/liken-machine-operator ../image/oci.sh
	DIST=$(abspath $(DIST)) LIKEN_VERSION=$(LIKEN_VERSION) ../image/oci.sh liken-machine-operator liken.sh/machine-operator

.PHONY: all clean

clean:
	rm -rf dist
