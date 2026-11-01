# The userland domain: the static programs that every image carries,
# built by buildroot from pinned source in a pinned container
# (build.sh documents the build, and fetch.sh the buildroot pin).
#
# One build writes every program, so one grouped rule names them all.
# Its prerequisites are the files of the BR2_EXTERNAL tree that the OS
# build reads and the image table, because each one changes what the
# build writes. bluetoothd.config and bluetooth/ are the bluetoothd
# image's, and the OS build does not select them. The table holds
# every outside image, so a change to any entry rebuilds this domain,
# which is slower than needed but never stale.
INPUTS := VERSION fetch.sh build.sh buildroot.sh common.config os.config \
	external.desc Config.in external.mk versions.mk wpa_supplicant.config \
	$(shell find patches iscsi -type f) ../../toolchains/images.toml

OUTPUTS := dist/sbin/mke2fs dist/sbin/mount.nfs dist/sbin/iscsid \
	dist/sbin/iscsiadm dist/sbin/wpa_supplicant \
	dist/zoneinfo/UTC dist/versions dist/sbom.cdx.json

$(OUTPUTS) &: $(INPUTS)
	./build.sh

.PHONY: all clean

all: $(OUTPUTS)

# clean keeps the download and compiler caches, which only cost time
# and bandwidth to fill again.
clean:
	rm -rf _build dist
