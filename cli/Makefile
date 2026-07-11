# The cli domain: the liken toolkit, the one program here that runs on
# an operator's workstation instead of inside a machine (main.go's
# header comment explains what it is and why the logic lives in the
# domain packages it dispatches to).

# The same version stamp every liken binary carries (see
# init/Makefile for the full explanation of the -X mechanism); the
# toolkit reports it via `liken version`, which matters once the
# binary ships with releases and outlives any one checkout.
LIKEN_VERSION ?= $(strip $(file <../VERSION))
DIST ?= dist

# Static for the same practical reason as everything else here: the
# toolkit ships in a release tarball and has to run on whatever Linux
# the operator has, with no libc expectations. -trimpath keeps this
# machine's filesystem layout out of the binary.
$(DIST)/liken: ../go.mod ../go.sum $(wildcard *.go) \
		$(wildcard ../identity/*.go) $(wildcard ../machine/*.go) \
		$(wildcard ../image/*.go) $(wildcard ../releases/*.go) \
		$(wildcard ../disks/*.go) $(wildcard ../scaffold/*.go) $(wildcard ../scaffold/*.tmpl) ../VERSION
	CGO_ENABLED=0 go build -trimpath \
		-ldflags "-X github.com/liken-sh/liken/machine.Version=$(LIKEN_VERSION)" \
		-o $(DIST)/liken .

.PHONY: all clean

all: $(DIST)/liken

clean:
	rm -rf dist
