# The systemd-boot domain: the boot menu on liken's install media,
# vendored prebuilt from Ubuntu's archive. One pinned version,
# fetched and verified by fetch.sh (documented there — including why
# install media needs a boot program when installed machines don't).
# override keeps this pin authoritative even under a release build:
# `make release VERSION=<stamp>` passes VERSION on the command line,
# and command-line variables silently replace plain assignments in
# every sub-make they reach. This domain's version comes from the
# VERSION file beside this Makefile, never from a caller.
override VERSION := $(strip $(file <VERSION))

dist/$(VERSION)/systemd-bootx64.efi: VERSION fetch.sh
	./fetch.sh

.PHONY: all clean

all: dist/$(VERSION)/systemd-bootx64.efi

clean:
	rm -rf dist
