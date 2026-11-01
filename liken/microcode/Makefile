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
# so liken/Makefile orders that domain before this one. The
# linux-firmware pin is a prerequisite here, because the output path
# names only the Intel release: after a linux-firmware bump the old
# cpio is still at the same path, and without that pin make reports
# it as up to date with the old AMD files in it. The CI cache key in
# .github/actions/build-setup hashes the same files.
dist/$(VERSION)/microcode.cpio: VERSION pin.toml fetch.sh ../linux-firmware/VERSION
	./fetch.sh

.PHONY: all clean

all: dist/$(VERSION)/microcode.cpio

clean:
	rm -rf dist
