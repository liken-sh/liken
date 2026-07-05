# The init domain: liken itself, the only program here that isn't
# vendored. main.go's header comment explains what it is.

# The liken release this binary carries, stamped into the machine
# package at link time (-X overwrites a package variable in the final
# binary; no generated files, no build step). It surfaces on the
# cluster as the Machine's status.version.liken.
VERSION := $(strip $(file <../VERSION))

# CGO_ENABLED=0 forbids C bindings, which makes Go emit a fully static
# binary: no libc, no dynamic loader. That's not an optimization here,
# it's a requirement: the initramfs contains no libraries to load, and
# PID 1 has no one to ask for them. -trimpath keeps this machine's
# filesystem layout out of the binary, a small step toward reproducible
# builds.
dist/liken: ../go.mod ../go.sum $(wildcard *.go) $(wildcard ../machine/*.go) ../VERSION
	CGO_ENABLED=0 go build -trimpath \
		-ldflags "-X github.com/chrisguidry/liken/machine.Version=$(VERSION)" \
		-o dist/liken .

.PHONY: all clean

all: dist/liken

clean:
	rm -rf dist
