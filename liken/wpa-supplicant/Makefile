# The wpa-supplicant domain: the wireless supplicant, one static
# binary that fetch.sh builds from pinned source inside a pinned
# container (plans/62-wifi.md). There is no daemon manifest and no OCI
# image here, because init supervises this process on the host and no
# pod ever runs it. The binary ships at /sbin in every image.
#
# override keeps this pin authoritative even under a release build.
# `make release VERSION=<stamp>` passes VERSION on the command line,
# and command-line variables silently replace plain assignments in
# every sub-make they reach. This domain's version always comes from
# the VERSION file beside this Makefile, never from a caller.
override VERSION := $(strip $(file <VERSION))

dist/$(VERSION)/wpa_supplicant: VERSION build.config fetch.sh
	./fetch.sh

.PHONY: all clean

all: dist/$(VERSION)/wpa_supplicant

clean:
	rm -rf dist
