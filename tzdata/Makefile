# The tzdata domain: the IANA timezone database, compiled from IANA's
# own source inside a pinned container, the way open-iscsi and
# nfs-utils are built. fetch.sh documents why liken compiles this one
# instead of downloading a compiled tree.
#
# override keeps this pin authoritative even under a release build.
# `make release VERSION=<stamp>` passes VERSION on the command line,
# and command-line variables silently replace plain assignments in
# every sub-make they reach. This domain's version always comes from
# the VERSION file beside this Makefile, never from a caller.
override VERSION := $(strip $(file <VERSION))

# UTC stands for the whole tree. zic writes all 604 files in one run,
# so any one of them shows the run finished. UTC is the one name that
# no tz release will rename.
dist/$(VERSION)/zoneinfo/UTC: VERSION DIGESTS tz.asc fetch.sh
	./fetch.sh

.PHONY: all clean

all: dist/$(VERSION)/zoneinfo/UTC

# Rebuilding is cheap because the verified tarballs stay cached, so
# clean removes every built tree. clean keeps the cache: deleting it
# would only cost bandwidth to rebuild.
clean:
	rm -rf dist
