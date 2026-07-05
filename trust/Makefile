# The trust domain: the CA certificates this machine trusts, vendored
# like everything else: one pinned snapshot, fetched and verified by
# fetch.sh (documented there).
VERSION := $(strip $(file <VERSION))

dist/$(VERSION)/cacert.pem: VERSION fetch.sh
	./fetch.sh

.PHONY: all clean

all: dist/$(VERSION)/cacert.pem

clean:
	rm -rf dist
