# The mount domain: liken's mount(8), the program Kubernetes runs
# when a pod needs a volume. main.go's header comment explains what
# it is and why an OS this small has to ship one.
#
# This binary carries no version stamp. It is not a component anyone
# asks the version of: it holds no state, exposes no interface to the
# cluster, and reports nothing about itself. The liken version the
# machine reports covers it, the same way it covers init.

# CGO_ENABLED=0 forbids C bindings, which makes Go emit a fully
# static binary: no libc, no dynamic loader. The image ships no
# shared libraries, so a dynamically linked program would have
# nothing to load. -trimpath keeps this machine's filesystem layout
# out of the binary, a small step toward reproducible builds.
DIST ?= dist

$(DIST)/mount: ../go.mod ../go.sum $(wildcard *.go)
	CGO_ENABLED=0 go build -trimpath -o $(DIST)/mount .

.PHONY: all clean

all: $(DIST)/mount

clean:
	rm -rf dist
