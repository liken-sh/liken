# Builds the `liken` program, which runs as PID 1. main.go describes
# what it does.

# When a recipe fails or the build is killed, the target can be left
# partly written and newer than its inputs. make deletes it, so the
# next build makes it again.
.DELETE_ON_ERROR:

# version.mk sets LIKEN_VERSION from `git describe`, and a stamp file
# that changes when the version changes, so the binary relinks. The
# link step writes LIKEN_VERSION into machine.Version, which the
# Machine reports as status.version.liken. The releases build passes
# its own LIKEN_VERSION and DIST.
include ../version.mk
DIST ?= dist

# A drill release sets this to panic or wedge-k3s, so that the release
# fails on purpose (see fault.go). Every other build leaves it empty.
LIKEN_FAULT ?=

# CGO_ENABLED=0 makes a static binary. PID 1 needs one because the
# initramfs has no libc and no dynamic loader. -trimpath keeps the
# build machine's paths out of the binary.
$(DIST)/liken: ../go.mod ../go.sum $(wildcard *.go) \
		$(wildcard ../api/*.go) $(wildcard ../cluster/*.go) \
		$(wildcard ../machine/*.go) $(wildcard ../disks/*.go) \
		$(wildcard ../hardware/*.go) $(LIKEN_VERSION_STAMP)
	CGO_ENABLED=0 go build -trimpath \
		-ldflags "-X github.com/liken-sh/liken/liken/machine.Version=$(LIKEN_VERSION) -X main.fault=$(LIKEN_FAULT)" \
		-o $(DIST)/liken .

.PHONY: all clean

all: $(DIST)/liken

clean:
	rm -rf dist
