# The cluster operator domain: the fleet-level half of operating the
# OS through the Kubernetes API.
#
# Two artifacts, on exactly the machine operator's pattern: the
# static binary and the OCI image tarball image/oci.sh wraps around
# it. main.go's header comment explains what this program watches,
# what it writes, and why it needs no leader election.
include ../version.mk
DIST ?= dist

all: $(DIST)/liken-cluster-operator-image.tar

$(DIST)/liken-cluster-operator: ../go.mod ../go.sum $(wildcard *.go) \
		$(wildcard ../machine/*.go) $(wildcard ../kubernetes/*.go) $(LIKEN_VERSION_STAMP)
	CGO_ENABLED=0 go build -trimpath \
		-ldflags "-X github.com/liken-sh/liken/machine.Version=$(LIKEN_VERSION)" \
		-o $(DIST)/liken-cluster-operator .

$(DIST)/liken-cluster-operator-image.tar: $(DIST)/liken-cluster-operator ../image/oci.sh
	DIST=$(abspath $(DIST)) LIKEN_VERSION=$(LIKEN_VERSION) ../image/oci.sh liken-cluster-operator liken.sh/cluster-operator

.PHONY: all clean

clean:
	rm -rf dist
