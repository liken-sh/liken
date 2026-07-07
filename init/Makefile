# The init domain: liken itself, the only program here that isn't
# vendored. main.go's header comment explains what it is.

# The liken release this binary carries, stamped into the machine
# package at link time (-X overwrites a package variable in the final
# binary; no generated files, no build step). It surfaces on the
# cluster as the Machine's status.version.liken. Both the version and
# the output directory are overridable (?=) because the releases
# domain rebuilds this same program under a different version stamp
# into its own tree. An ordinary build and a release build differ
# only in these two inputs, never in the rules.
LIKEN_VERSION ?= $(strip $(file <../VERSION))
DIST ?= dist

# The fault knob, for releases deliberately built to fail (fault.go):
# empty for every real build, panic or wedge-k3s for the drills.
LIKEN_FAULT ?=

# CGO_ENABLED=0 forbids C bindings, which makes Go emit a fully static
# binary: no libc, no dynamic loader. That's not an optimization here,
# it's a requirement: the initramfs contains no shared libraries, so
# a dynamically linked PID 1 would have nothing to load. -trimpath
# keeps this machine's filesystem layout out of the binary, a small
# step toward reproducible builds.
$(DIST)/liken: ../go.mod ../go.sum $(wildcard *.go) $(wildcard ../machine/*.go) ../VERSION
	CGO_ENABLED=0 go build -trimpath \
		-ldflags "-X github.com/chrisguidry/liken/machine.Version=$(LIKEN_VERSION) -X main.fault=$(LIKEN_FAULT)" \
		-o $(DIST)/liken .

.PHONY: all clean

all: $(DIST)/liken

clean:
	rm -rf dist
