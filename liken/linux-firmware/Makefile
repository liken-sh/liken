# The linux-firmware domain: the driver blobs the kernel can request
# at probe time, vendored like the kernel and hwdata. One pinned
# release, fetched and verified by fetch.sh, then cut down to the
# set this kernel build can actually ask for by derive.sh (both
# documented there).
# override keeps this pin authoritative even under a release build.
# `make release VERSION=<stamp>` passes VERSION on the command line,
# and command-line variables silently replace plain assignments in
# every sub-make they reach. This domain's version always comes from
# the VERSION file beside this Makefile, never from a caller.
override VERSION := $(strip $(file <VERSION))

# The derived set is a function of two pins: this domain's release
# and the kernel build whose modules declare the names. The kernel
# pin is a prerequisite here, because the output path names only this
# domain's release: after a kernel bump the old derived set is still
# at the same path, and without the kernel pin make reports it as up
# to date. The root Makefile orders the kernel build before this one.
# The CI cache key in .github/actions/build-setup hashes the same
# files.
dist/$(VERSION)/derived.txt: VERSION fetch.sh derive.sh ../kernel/VERSION
	./fetch.sh
	./derive.sh

.PHONY: all clean

all: dist/$(VERSION)/derived.txt

clean:
	rm -rf dist
