# The microcode domain: the CPU microcode early cpio, vendored like
# the kernel and linux-firmware. One pinned Intel release plus the
# AMD families from the linux-firmware tree, fetched, verified, and
# assembled by fetch.sh (documented there).
# override keeps this pin authoritative even under a release build.
# `make release VERSION=<stamp>` passes VERSION on the command line,
# and command-line variables silently replace plain assignments in
# every sub-make they reach. This domain's version always comes from
# the VERSION file beside this Makefile, never from a caller.
override VERSION := $(strip $(file <VERSION))

# The AMD half comes from the linux-firmware domain's fetched tree,
# so the root Makefile orders that domain before this one.
dist/$(VERSION)/microcode.cpio: VERSION fetch.sh
	./fetch.sh

.PHONY: all clean

all: dist/$(VERSION)/microcode.cpio

clean:
	rm -rf dist
