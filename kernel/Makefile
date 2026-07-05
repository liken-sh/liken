# The kernel domain: liken does not build Linux, it vendors Linux.
# Canonical's mainline archive publishes every upstream release as
# pre-built packages, and fetch.sh (documented there) turns one
# pinned version of those into the two things the rest of the build
# needs: a bootable vmlinuz and an indexed module tree.

# The pin is a file, not a variable, so a kernel upgrade is a one-line,
# obviously-reviewable diff. $(file <...) reads it at parse time; strip
# drops the trailing newline.
VERSION := $(strip $(file <VERSION))

# vmlinuz stands in for the whole extraction: modules, config, and the
# release string all arrive in the same fetch.sh run. Make only fires
# this when the pin or the script changes; fetch.sh's own download cache
# makes even those re-runs cheap.
dist/$(VERSION)/vmlinuz: VERSION fetch.sh
	./fetch.sh

.PHONY: all clean

all: dist/$(VERSION)/vmlinuz

# Extracted kernels are cheap to recreate (the downloads are cached), so
# clean sweeps them all; the cache is precious bandwidth and survives.
clean:
	rm -rf dist
