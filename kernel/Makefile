# The kernel domain. liken vendors Linux instead of building it.
# Canonical's mainline archive publishes every upstream release as
# pre-built packages. fetch.sh (documented there) extracts one pinned
# version of those packages into the two things the rest of the build
# needs: a bootable vmlinuz and an indexed module tree.

# The pin is a file, not a variable, so a kernel upgrade shows as a
# one-line, reviewable diff. $(file <...) reads the file at parse
# time; strip removes the trailing newline.
# override keeps this pin authoritative even under a release build.
# `make release VERSION=<stamp>` passes VERSION on the command line,
# and command-line variables silently replace plain assignments in
# every sub-make they reach. This domain's version always comes from
# the VERSION file beside this Makefile, never from a caller.
override VERSION := $(strip $(file <VERSION))

# vmlinuz represents the whole extraction: modules, config, and the
# release string all arrive in the same fetch.sh run. Make runs this
# rule only when the pin or the script changes, and fetch.sh's own
# download cache keeps even those re-runs cheap.
dist/$(VERSION)/vmlinuz: VERSION fetch.sh
	./fetch.sh

.PHONY: all clean

all: dist/$(VERSION)/vmlinuz

# Extracted kernels are cheap to recreate because the downloads are
# cached, so clean removes them all. clean keeps the cache: deleting it
# would only cost bandwidth to rebuild.
clean:
	rm -rf dist
