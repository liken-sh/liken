# The logs domain: the relays that carry the machine's host-level log
# streams into the cluster.
#
# Two artifacts, on the operator's exact pattern: the liken-logs
# binary (a static Go build; main.go explains the four relays it
# carries) and the OCI image tarball that delivers it (image.sh). The
# four DaemonSets in manifests/ all run this one image, differing
# only in the verb they pass and the host paths they mount.
#
# The version and output directory are overridable for the same
# reason the operator's are: the releases domain rebuilds these
# relays under a different version stamp into its own tree, through
# these same rules.
LIKEN_VERSION ?= $(strip $(file <../VERSION))
DIST ?= dist

all: $(DIST)/liken-logs-image.tar

$(DIST)/liken-logs: ../go.mod ../go.sum $(wildcard *.go) $(wildcard ../machine/*.go) ../VERSION
	CGO_ENABLED=0 go build -trimpath \
		-ldflags "-X github.com/chrisguidry/liken/machine.Version=$(LIKEN_VERSION)" \
		-o $(DIST)/liken-logs .

$(DIST)/liken-logs-image.tar: $(DIST)/liken-logs image.sh
	DIST=$(abspath $(DIST)) LIKEN_VERSION=$(LIKEN_VERSION) ./image.sh

.PHONY: all clean

clean:
	rm -rf dist
