# The nfs-utils domain: the NFS client, the host half of the nfs
# feature (plans/17-network-storage-clients.md).
#
# One artifact: the static mount.nfs that fetch.sh builds from pinned
# source inside a pinned container. The kernel's mount path runs it as
# the nfs filesystem's mount helper, so it ships at /sbin in every
# image, under both of its names (mount -t nfs4 looks for
# mount.nfs4). There is no daemon, no manifest, and no OCI image.
# NFSv4 needs none of them, which is most of why the feature is v4
# only.
# override keeps this pin authoritative even under a release build.
# `make release VERSION=<stamp>` passes VERSION on the command line,
# and command-line variables silently replace plain assignments in
# every sub-make they reach. This domain's version always comes from
# the VERSION file beside this Makefile, never from a caller.
override VERSION := $(strip $(file <VERSION))

dist/$(VERSION)/mount.nfs: VERSION fetch.sh
	./fetch.sh

.PHONY: all clean

all: dist/$(VERSION)/mount.nfs

clean:
	rm -rf dist
