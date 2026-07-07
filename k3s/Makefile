# The k3s domain. liken vendors k3s rather than building Kubernetes:
# one pinned version, one static binary, fetched and verified by
# fetch.sh (documented there).
VERSION := $(strip $(file <VERSION))

dist/$(VERSION)/k3s: VERSION fetch.sh
	./fetch.sh

.PHONY: all clean

all: dist/$(VERSION)/k3s

clean:
	rm -rf dist
