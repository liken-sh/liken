# The userland domain: the static programs that every image carries,
# built by buildroot from pinned source in a pinned container
# (build.sh documents the build, fetch.sh the buildroot pin, and
# artifact.sh the registry that holds finished builds).
#
# One build writes every program, so one grouped rule names them all.
# Its prerequisites are the files of the BR2_EXTERNAL tree that the OS
# build reads and the image table, because each one changes what the
# build writes. bluetoothd.config and bluetooth/ are the bluetoothd
# image's, and the OS build does not select them. The table holds
# every outside image, so a change to any entry runs the rule, and
# artifact.sh keeps dist/ when the `buildroot` entry did not change.
#
# The rule takes a finished build of the same inputs from the registry
# when one is there, and builds with buildroot when none is. After a
# build, artifact.sh records the key of the inputs in dist/key, and in
# CI on main it pushes dist/ to the registry.
INPUTS := VERSION fetch.sh build.sh artifact.sh buildroot.sh common.config os.config \
	external.desc Config.in external.mk versions.mk wpa_supplicant.config \
	$(shell find patches iscsi -type f) ../../toolchains/images.toml

OUTPUTS := dist/sbin/mke2fs dist/sbin/mount.nfs dist/sbin/iscsid \
	dist/sbin/iscsiadm dist/sbin/wpa_supplicant \
	dist/zoneinfo/UTC dist/versions dist/sbom.cdx.json

$(OUTPUTS) &: $(INPUTS)
	./artifact.sh restore $(OUTPUTS) || { ./build.sh && ./artifact.sh save; }

.PHONY: all clean

all: $(OUTPUTS)

# clean keeps the download and compiler caches, which only cost time
# and bandwidth to fill again.
clean:
	rm -rf _build dist
