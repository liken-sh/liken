# The kernel domain. liken vendors Linux rather than building it:
# Canonical's mainline archive publishes every upstream release as
# pre-built packages, and fetch.sh (documented there) extracts one
# pinned version of those into the two things the rest of the build
# needs: a bootable vmlinuz and an indexed module tree.

# The pin is a file, not a variable, so a kernel upgrade is a one-line,
# obviously-reviewable diff. $(file <...) reads it at parse time; strip
# drops the trailing newline.
# override keeps this pin authoritative even under a release build:
# `make release VERSION=<stamp>` passes VERSION on the command line,
# and command-line variables silently replace plain assignments in
# every sub-make they reach. This domain's version comes from the
# VERSION file beside this Makefile, never from a caller.
override VERSION := $(strip $(file <VERSION))

# vmlinuz represents the whole extraction: modules, config, and the
# release string all arrive in the same fetch.sh run. Make only runs
# this when the pin or the script changes, and fetch.sh's own download
# cache makes even those re-runs cheap.
dist/$(VERSION)/vmlinuz: VERSION fetch.sh
	./fetch.sh

.PHONY: all clean

all: dist/$(VERSION)/vmlinuz

# Extracted kernels are cheap to recreate because the downloads are
# cached, so clean removes them all. The cache itself is kept: deleting
# it would only cost bandwidth to rebuild.
clean:
	rm -rf dist
