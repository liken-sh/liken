# The k3s domain. liken vendors k3s rather than building Kubernetes:
# one pinned version, one static binary, fetched and verified by
# fetch.sh (documented there).
# override keeps this pin authoritative even under a release build:
# `make release VERSION=<stamp>` passes VERSION on the command line,
# and command-line variables silently replace plain assignments in
# every sub-make they reach. This domain's version comes from the
# VERSION file beside this Makefile, never from a caller.
override VERSION := $(strip $(file <VERSION))

dist/$(VERSION)/k3s: VERSION fetch.sh
	./fetch.sh

.PHONY: all clean

all: dist/$(VERSION)/k3s

clean:
	rm -rf dist
