# The hwdata domain: the PCI naming database, vendored like the
# kernel, the trust store, and mke2fs. One pinned snapshot, fetched
# and verified by fetch.sh (documented there).
# override keeps this pin authoritative even under a release build.
# `make release VERSION=<stamp>` passes VERSION on the command line,
# and command-line variables silently replace plain assignments in
# every sub-make they reach. This domain's version always comes from
# the VERSION file beside this Makefile, never from a caller.
override VERSION := $(strip $(file <VERSION))

dist/$(VERSION)/pci.ids: VERSION fetch.sh
	./fetch.sh

.PHONY: all clean

all: dist/$(VERSION)/pci.ids

clean:
	rm -rf dist
