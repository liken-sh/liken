# The e2fsprogs domain: the filesystem-creation userspace, vendored
# like the kernel, k3s, and xtables: one pinned version, one static
# binary, fetched and verified by fetch.sh (documented there).
VERSION := $(strip $(file <VERSION))

dist/$(VERSION)/mke2fs: VERSION fetch.sh
	./fetch.sh

.PHONY: all clean

all: dist/$(VERSION)/mke2fs

clean:
	rm -rf dist
