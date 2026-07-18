# The init domain: liken itself, the only program here that isn't
# vendored. main.go's header comment explains what it is.

# The liken version this binary carries, stamped into the machine
# package at link time (-X overwrites a package variable in the final
# binary; no generated files, no build step). It surfaces on the
# cluster as the Machine's status.version.liken. version.mk supplies
# LIKEN_VERSION (the git-described commit) and the stamp file that
# lets Make notice it changing; the releases domain overrides the
# version and the output directory to rebuild this same program under
# a release's name into its own tree. An ordinary build and a release
# build differ only in those two inputs, never in the rules.
include ../version.mk
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
$(DIST)/liken: ../go.mod ../go.sum $(wildcard *.go) $(wildcard ../machine/*.go) $(wildcard ../disks/*.go) $(wildcard ../hardware/*.go) $(LIKEN_VERSION_STAMP)
	CGO_ENABLED=0 go build -trimpath \
		-ldflags "-X github.com/liken-sh/liken/machine.Version=$(LIKEN_VERSION) -X main.fault=$(LIKEN_FAULT)" \
		-o $(DIST)/liken .

.PHONY: all clean

all: $(DIST)/liken

clean:
	rm -rf dist
