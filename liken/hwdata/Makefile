# The hwdata domain: the PCI naming database, vendored like the
# kernel, the trust store, and mke2fs. One pinned snapshot, fetched
# and verified by fetch.sh (documented there).
# override keeps this pin authoritative even under a release build.
# `make release VERSION=<stamp>` passes VERSION on the command line,
# and command-line variables silently replace plain assignments in
# every sub-make they reach. This domain's version always comes from
# the VERSION file beside this Makefile, never from a caller.
override VERSION := $(strip $(file <VERSION))

# A recipe that fails, or a build that is killed during one, leaves its
# target partly written and newer than its inputs. make deletes that
# target, so the next build makes it again and does not take it as
# current.
.DELETE_ON_ERROR:

dist/$(VERSION)/pci.ids: VERSION pin.toml fetch.sh
	./fetch.sh

.PHONY: all clean

all: dist/$(VERSION)/pci.ids

clean:
	rm -rf dist
