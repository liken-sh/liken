# The operator domain: the in-cluster half of the Machine API.
#
# Two artifacts: the operator binary (a static Go build, same
# discipline as init's) and the OCI image tarball that carries it into
# the cluster (the story is in image.sh). main.go opens with what an
# operator is and why this one uses no Kubernetes libraries.

VERSION := $(strip $(file <../VERSION))

all: dist/liken-operator-image.tar

dist/liken-operator: ../go.mod ../go.sum $(wildcard *.go) $(wildcard ../machine/*.go) ../VERSION
	CGO_ENABLED=0 go build -trimpath \
		-ldflags "-X github.com/chrisguidry/liken/machine.Version=$(VERSION)" \
		-o dist/liken-operator .

dist/liken-operator-image.tar: dist/liken-operator image.sh
	./image.sh

.PHONY: all clean

clean:
	rm -rf dist
