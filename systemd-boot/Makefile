# The systemd-boot domain: the boot menu on liken's install media,
# vendored prebuilt from Ubuntu's archive. One pinned version,
# fetched and verified by fetch.sh (documented there — including why
# install media needs a boot program when installed machines don't).
VERSION := $(strip $(file <VERSION))

dist/$(VERSION)/systemd-bootx64.efi: VERSION fetch.sh
	./fetch.sh

.PHONY: all clean

all: dist/$(VERSION)/systemd-bootx64.efi

clean:
	rm -rf dist
