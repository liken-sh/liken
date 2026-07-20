# The xtables domain: the netfilter userspace, vendored like the
# kernel and k3s. One pinned version, static binaries, fetched and
# verified by fetch.sh (documented there).
# override keeps this pin authoritative even under a release build.
# `make release VERSION=<stamp>` passes VERSION on the command line,
# and command-line variables silently replace plain assignments in
# every sub-make they reach. This domain's version always comes from
# the VERSION file beside this Makefile, never from a caller.
override VERSION := $(strip $(file <VERSION))

dist/$(VERSION)/bin/xtables-legacy-multi: VERSION fetch.sh
	./fetch.sh

.PHONY: all clean

all: dist/$(VERSION)/bin/xtables-legacy-multi

clean:
	rm -rf dist
