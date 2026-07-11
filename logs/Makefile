# The logs domain: the relays that carry the machine's host-level log
# streams into the cluster.
#
# Two artifacts, on the operator's exact pattern: the liken-logs
# binary (a static Go build; main.go explains the four relays it
# carries) and the OCI image tarball that delivers it, packaged by
# the shared recipe (image/oci.sh). The machine-logs DaemonSet in
# manifests/ runs this one image in four containers, differing only
# in the verb they pass and the host paths they mount.
#
# version.mk supplies the version stamp; the releases domain
# overrides it and the output directory to rebuild these relays under
# a release's name into its own tree, through these same rules.
include ../version.mk
DIST ?= dist

all: $(DIST)/liken-logs-image.tar

$(DIST)/liken-logs: ../go.mod ../go.sum $(wildcard *.go) $(wildcard ../machine/*.go) $(LIKEN_VERSION_STAMP)
	CGO_ENABLED=0 go build -trimpath \
		-ldflags "-X github.com/liken-sh/liken/machine.Version=$(LIKEN_VERSION)" \
		-o $(DIST)/liken-logs .

$(DIST)/liken-logs-image.tar: $(DIST)/liken-logs ../image/oci.sh
	DIST=$(abspath $(DIST)) LIKEN_VERSION=$(LIKEN_VERSION) ../image/oci.sh liken-logs liken.sh/logs

.PHONY: all clean

clean:
	rm -rf dist
