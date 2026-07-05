# The xtables domain: the netfilter userspace, vendored like the
# kernel and k3s — one pinned version, static binaries, fetched and
# verified by fetch.sh (the story lives there).
VERSION := $(strip $(file <VERSION))

dist/$(VERSION)/bin/xtables-legacy-multi: VERSION fetch.sh
	./fetch.sh

.PHONY: all clean

all: dist/$(VERSION)/bin/xtables-legacy-multi

clean:
	rm -rf dist
